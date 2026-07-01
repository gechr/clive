// Package notify performs a passive, never-blocking "you're behind" check for a
// Go CLI and prints a one-line hint when a newer release ref exists.
//
// A ref is an opaque release identifier: usually a semver tag, but it may be a
// commit hash or another caller-defined token when [WithLatestFunc] and
// [WithComparator] are supplied. The hint is served from a small cache file - an
// instant disk read, never the network - so it adds no latency to the command.
// When that cache is stale, a background goroutine refreshes it; the refresh is
// abandoned at process exit unless the host calls [AwaitRefresh]. The flush
// re-reads the cache, so a refresh that finishes while the command runs is shown
// that same run; otherwise its result appears on the next invocation. The
// auto-printed hint is itself throttled to once per notify interval (default 24h,
// [WithNotifyInterval]), independent of the refresh interval (default 24h,
// [WithRefreshInterval]); either interval set non-positive disables its throttle.
// A tool describes itself with an [updater.Tool] - the same Config its
// self-update uses, such as a brew.Config, goinstall.Config, or github.Config -
// calls [Check] before dispatching its command, and invokes the returned flush
// function after:
//
//	flush := notify.Check(brew.New(clive.Info{Module: "github.com/example/myapp"}))
//	defer flush()
//
// The per-tool kill switch MYAPP_NO_UPDATE_CHECK (derived from the binary name)
// is a global opt-out: [Check] and [Pending] do not schedule a refresh and
// [Pending] reports no update. A non-terminal stderr only suppresses the
// auto-printed hint returned by [Check]; it does not gate [Pending].
//
// The verdict is failure-tolerant: a background refresh that fails re-stamps the
// last successfully fetched ref to honour the cooldown and records the failure,
// but a known, still-valid newer ref keeps reporting pending. The failure governs
// only refresh scheduling and the cooldown, never whether an update is shown, so a
// transient outage cannot silence an update the tool already discovered. A check
// that has never succeeded has no cached ref and so reports nothing.
//
// [Pending] exposes the same verdict without printing so hosts can render update
// state in their own UI. Its [Result] also carries Failed (the last refresh
// failed, so the ref may be stale) and Dismissed (the ref was dismissed via
// [Skip]), letting a host distinguish no update, a known-but-stale update, and a
// dismissed update. [Skip] records a dismissed ref in the same cache. The
// dismissal suppresses hints only while the latest known ref is exactly the
// dismissed ref; when the latest ref changes, normal comparator-based behavior
// resumes, including for non-semver refs where ordering is undefined. The cache
// file remains mtime-based for cooldowns. New writes use a JSON object holding
// the active track, latest ref, dismissed ref, and last-refresh failure state;
// old single-line files are read as "latest ref for this cache file's active
// track, no dismissal".
//
// By default "latest" is the highest semver tag in the tool's GitHub repository,
// compared with semver ordering and the existing dev-build base-tag rule. A tool
// whose releases are not readable GitHub tags - a private repo, rolling commit
// hashes, or an artifact bucket - overrides lookup, comparison, display, and
// track with [WithLatestFunc], [WithComparator], [WithRefDisplay], and
// [WithChannel] while keeping the same cache and cooldown behavior.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/gechr/x/terminal"
)

const (
	// defaultRefreshInterval is the minimum gap between network refreshes. The
	// cache mtime records the last refresh, so the network is hit at most once per
	// interval. [WithRefreshInterval] overrides it.
	defaultRefreshInterval = 24 * time.Hour

	// defaultNotifyInterval is the minimum gap between repeat auto-printed hints.
	// A pending update is surfaced at most once per interval rather than on every
	// invocation; [WithNotifyInterval] overrides it, and a non-positive interval
	// disables the throttle so every run prints.
	defaultNotifyInterval = 24 * time.Hour

	// lookupTimeout bounds the background tag fetch.
	lookupTimeout = 2 * time.Second

	// stampPerm is the mode of the cache file.
	stampPerm = 0o644

	// dirPerm is the mode of the per-tool cache directory.
	dirPerm = 0o755

	// refreshStampName is the cache file under the per-tool cache directory. Its
	// content records the latest known ref and dismissed ref; its mtime is when
	// that was last refreshed.
	refreshStampName = "last-update-check"

	// notifyStampName is the marker file whose mtime records when the auto-printed
	// hint was last shown. It is kept separate from refreshStampName so recording a
	// hint never disturbs the refresh mtime, and is empty - only its mtime matters.
	notifyStampName = "last-update-notify"

	// stampVersion is the current structured cache file format.
	stampVersion = 1

	// envSuffix is appended to the upper-cased binary name to form the kill
	// switch: "myapp" becomes "MYAPP_NO_UPDATE_CHECK".
	envSuffix = "_NO_UPDATE_CHECK"

	// updateColor is the orange (256-colour 208) accent for the update message.
	updateColor = "208"
)

var refreshes = newRefreshTracker()

// Check serves an "update available" hint from the per-tool cache and, when that
// cache is stale, refreshes it in the background for the next invocation. It
// never blocks and never touches the network on the calling path. tool describes
// the tool: its binary name forms the kill-switch env var, the cache namespace,
// and the `<binary> update` command, and its display name opens the message.
//
// The returned function prints the hint; the caller invokes it after its command
// completes, so the hint follows the command's own output. It is a no-op when no
// update is pending, when the check is disabled, or when stderr is not a
// terminal. Non-terminal stderr suppresses only printing; stale caches may still
// refresh in the background.
func Check(tool updater.Tool, opts ...Option) func() {
	c := newChecker(tool, opts...)

	if os.Getenv(c.envVar()) != "" {
		return func() {}
	}

	flush := c.hint()
	if !terminal.Is(os.Stderr) {
		return func() {}
	}
	return flush
}

// Result is the update verdict read from the notify cache.
type Result struct {
	// CurrentRef is the running build's raw ref.
	CurrentRef string
	// LatestRef is the latest raw ref known for Track.
	LatestRef string
	// LatestIsUpdate reports the comparator verdict before dismissal is applied.
	LatestIsUpdate bool
	// Failed reports that the last background refresh failed, so LatestRef may be
	// stale - it is the last successfully fetched ref, not a fresh lookup. A known
	// update is still reported through a transient failure; this flag only lets a
	// host annotate that the data behind it could not be confirmed this run.
	Failed bool
	// Dismissed reports that LatestRef was dismissed via [Skip] and still matches
	// the dismissed ref, so the update is suppressed. It distinguishes "you
	// dismissed this" from "no update". A newer LatestRef clears it.
	Dismissed bool
	// Track is the active release track name.
	Track string
	// CurrentDisplay is CurrentRef after [WithRefDisplay].
	CurrentDisplay string
	// LatestDisplay is LatestRef after [WithRefDisplay].
	LatestDisplay string
}

// Pending returns the cached update verdict without printing. The bool is
// LatestIsUpdate && !Dismissed: it reports whether there is an update to act on
// after the comparator, track, cache, and dismissal state are applied. It is not
// gated by a failed refresh - a known newer ref keeps reporting true through a
// transient failure - so consult [Result.Failed] to learn the data may be stale
// and [Result.Dismissed] to tell a dismissal from no update. It uses the same
// stale-cache background refresh path as [Check], but unlike the auto-printed
// hint it does not require a TTY.
func Pending(tool updater.Tool, opts ...Option) (Result, bool) {
	c := newChecker(tool, opts...)
	if os.Getenv(c.envVar()) != "" {
		return Result{}, false
	}
	c.scheduleRefresh()
	return c.pending()
}

// Skip dismisses ref on the active track until the latest known ref changes.
// The dismissal is persisted in the notify cache and survives process restarts.
func Skip(tool updater.Tool, ref string, opts ...Option) error {
	c := newChecker(tool, opts...)
	st, _, cached := c.readStamp()
	if !cached || st.Track != c.channel {
		st = stamp{Track: c.channel}
	}
	st.Track = c.channel
	st.Skipped = ref
	return c.saveStamp(st)
}

// AwaitRefresh waits for in-flight background refreshes to finish, returning
// true when there were none or all completed before timeout. It returns false
// when timeout elapses, and never waits longer than timeout.
func AwaitRefresh(timeout time.Duration) bool {
	return refreshes.wait(timeout)
}

// checker holds the resolved configuration for one check.
type checker struct {
	tool            updater.Tool
	current         string
	channel         string
	client          *http.Client
	cacheDir        string
	color           color.Color
	latest          LatestFunc
	comparator      func(current, latest string) bool
	display         func(string) string
	hintOpts        []updater.HintOption
	refreshInterval time.Duration
	notifyInterval  time.Duration
}

// stamp is the structured cache file format.
type stamp struct {
	Version int    `json:"version"`
	Track   string `json:"track"`
	Latest  string `json:"latest"`
	Skipped string `json:"skipped,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
}

// newChecker builds a checker with real-run defaults, then applies opts.
func newChecker(tool updater.Tool, opts ...Option) *checker {
	c := &checker{
		tool:       tool,
		current:    clive.Current(),
		client:     &http.Client{Timeout: lookupTimeout},
		cacheDir:   defaultCacheDir(tool.BinaryName()),
		color:      lipgloss.Color(updateColor),
		comparator: newer,
		display: func(s string) string {
			return s
		},
		refreshInterval: defaultRefreshInterval,
		notifyInterval:  defaultNotifyInterval,
	}
	// Default to the tool's own latest-ref lookup, reading c.client at call time so
	// a WithTransport seam still applies; WithLatestFunc replaces it wholesale.
	c.latest = func(ctx context.Context) (string, error) {
		return c.tool.LatestRef(ctx, c.client)
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// hint schedules a background refresh when the cache is missing or stale and
// returns a function that prints the "update available" line. The returned
// function re-reads the cache when invoked, so a refresh that completes while
// the command runs is reflected this run; otherwise the result appears on the
// next invocation. It performs no network I/O on the calling path.
func (c *checker) hint() func() {
	c.scheduleRefresh()

	return func() {
		res, ok := c.pending()
		if !ok || !c.notifyDue() {
			return
		}
		c.printHint(res)
		c.markNotified()
	}
}

// notifyDue reports whether enough time has elapsed since the last auto-printed
// hint for another to be shown. A non-positive interval, an absent cache, or an
// unreadable marker all mean due, so a hint that cannot record itself simply
// shows again next time.
func (c *checker) notifyDue() bool {
	if c.notifyInterval <= 0 || c.cacheDir == "" {
		return true
	}
	info, err := os.Stat(c.notifyPath())
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) >= c.notifyInterval
}

// markNotified stamps the notify marker's mtime to now, starting the interval
// before the next hint. The file content is irrelevant - only its mtime matters
// - and failures are ignored.
func (c *checker) markNotified() {
	if c.cacheDir == "" {
		return
	}
	if err := xos.EnsureDir(c.cacheDir, dirPerm); err != nil {
		return
	}
	_ = xos.AtomicWrite(c.notifyPath(), nil, stampPerm)
}

// scheduleRefresh starts a background refresh when the active track's cache is
// missing, stale, or belongs to another track.
func (c *checker) scheduleRefresh() {
	st, checkedAt, cached := c.readStamp()
	if !cached || st.Track != c.channel || time.Since(checkedAt) >= c.refreshInterval {
		c.startRefresh()
	}
}

// startRefresh runs refresh in the background and registers it for
// [AwaitRefresh].
func (c *checker) startRefresh() {
	refreshes.start()
	go func() {
		defer refreshes.done()
		c.refresh()
	}()
}

// shouldHint reports the latest cached tag and whether it is newer than the
// running build. It re-reads the cache, so a background refresh that has since
// completed is taken into account.
func (c *checker) shouldHint() (string, bool) {
	res, ok := c.pending()
	return res.LatestRef, ok
}

// pending reads the active track's cache and evaluates the shared update
// verdict used by Check and Pending.
func (c *checker) pending() (Result, bool) {
	res := Result{
		CurrentRef:     c.current,
		Track:          c.channel,
		CurrentDisplay: c.display(c.current),
	}

	st, _, cached := c.readStamp()
	if !cached || st.Track != c.channel {
		return res, false
	}
	res.LatestRef = st.Latest
	res.LatestDisplay = c.display(st.Latest)
	res.Failed = st.Failed
	// A check that has never succeeded leaves Latest empty, so the failure case on
	// a first run reports not-pending here regardless of res.Failed.
	if st.Latest == "" {
		return res, false
	}
	res.LatestIsUpdate = c.comparator(c.current, st.Latest)
	// Failure is deliberately not consulted below: a known, still-valid newer ref
	// keeps reporting pending through a transient refresh failure. st.Failed only
	// drives refresh scheduling and the cooldown re-stamp, recorded on Result.
	res.Dismissed = st.Skipped != "" && st.Latest == st.Skipped
	if res.Dismissed {
		return res, false
	}
	return res, res.LatestIsUpdate
}

// refresh fetches the latest ref and rewrites the cache. On failure it re-stamps
// the prior active-track value, so a failing check still respects the cooldown
// instead of refetching on every invocation. It is meant to run in its own
// goroutine.
func (c *checker) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	st, _, cached := c.readStamp()
	if !cached || st.Track != c.channel {
		st = stamp{Track: c.channel}
	}
	st.Track = c.channel

	latest, err := c.latest(ctx)
	if err != nil {
		clog.Debug().Err(err).Msg("Update check failed")
		st.Failed = true
		_ = c.saveStamp(st)
		return
	}
	st.Latest = latest
	st.Failed = false
	_ = c.saveStamp(st)
}

// readStamp returns the cached latest ref and when it was written. cached is
// false on the first run or when no cache directory is available.
func (c *checker) readStamp() (stamp, time.Time, bool) {
	if c.cacheDir == "" {
		return stamp{}, time.Time{}, false
	}
	path := c.stampPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return stamp{}, time.Time{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return stamp{}, time.Time{}, false
	}
	st, ok := c.decodeStamp(data)
	if !ok {
		return stamp{}, time.Time{}, false
	}
	return st, info.ModTime(), true
}

// decodeStamp reads both the current JSON cache and the old one-line format.
func (c *checker) decodeStamp(data []byte) (stamp, bool) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return stamp{Track: c.channel}, true
	}
	if strings.HasPrefix(trimmed, "{") {
		var st stamp
		if err := json.Unmarshal([]byte(trimmed), &st); err != nil {
			return stamp{}, false
		}
		return st, true
	}
	return stamp{Track: c.channel, Latest: trimmed}, true
}

// writeStamp records latest as the cache content for the active track, stamping
// the file's mtime to now. Failures are ignored: a check that cannot persist
// simply runs again next time, which is harmless.
func (c *checker) writeStamp(latest string) {
	st, _, cached := c.readStamp()
	if !cached || st.Track != c.channel {
		st = stamp{Track: c.channel}
	}
	st.Track = c.channel
	st.Latest = latest
	st.Failed = false
	_ = c.saveStamp(st)
}

// saveStamp persists st atomically using the cache mtime as the refresh stamp.
func (c *checker) saveStamp(st stamp) error {
	if c.cacheDir == "" {
		return fmt.Errorf("notify: no cache directory")
	}
	if err := xos.EnsureDir(c.cacheDir, dirPerm); err != nil {
		return err
	}
	st.Version = stampVersion
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return xos.AtomicWrite(c.stampPath(), data, stampPerm)
}

// stampPath returns the active track's refresh cache file.
func (c *checker) stampPath() string {
	return c.cacheFile(refreshStampName)
}

// notifyPath returns the active track's hint marker file.
func (c *checker) notifyPath() string {
	return c.cacheFile(notifyStampName)
}

// cacheFile returns base namespaced for the active track, so each track keeps an
// independent cache.
func (c *checker) cacheFile(base string) string {
	if c.channel == "" {
		return filepath.Join(c.cacheDir, base)
	}
	return filepath.Join(c.cacheDir, base+"-"+url.PathEscape(c.channel))
}

// refreshTracker tracks background refreshes without spawning wait goroutines.
type refreshTracker struct {
	mu       sync.Mutex
	inFlight int
	idle     chan struct{}
}

func newRefreshTracker() *refreshTracker {
	idle := make(chan struct{})
	close(idle)
	return &refreshTracker{idle: idle}
}

func (t *refreshTracker) start() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.inFlight == 0 {
		t.idle = make(chan struct{})
	}
	t.inFlight++
}

func (t *refreshTracker) done() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.inFlight--
	if t.inFlight == 0 {
		close(t.idle)
	}
}

func (t *refreshTracker) wait(timeout time.Duration) bool {
	t.mu.Lock()
	if t.inFlight == 0 {
		t.mu.Unlock()
		return true
	}
	idle := t.idle
	t.mu.Unlock()

	if timeout <= 0 {
		select {
		case <-idle:
			return true
		default:
			return false
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-idle:
		return true
	case <-timer.C:
		return false
	}
}

// envVar is the per-tool kill switch, such as "MYAPP_NO_UPDATE_CHECK".
func (c *checker) envVar() string {
	return strings.ToUpper(c.tool.BinaryName()) + envSuffix
}

// printHint logs the one-line "update available" hint: a leading blank line, the
// 💡 symbol, the installed and latest refs as fields, and a coloured message
// whose `<binary> update` command is bold.
func (c *checker) printHint(res Result) {
	installed, latest := c.hintRefs(res)
	fmt.Fprintln(os.Stderr)
	opts := append([]updater.HintOption{updater.WithOutdatedHintColor(c.color)}, c.hintOpts...)
	updater.NewOutdatedHint(opts...).
		Log(c.tool.DisplayName(), c.tool.BinaryName(), installed, latest)
}

// hintRefs returns the rendered installed/latest fields used by printHint, with a
// leading "v" stripped so they read like a from→to version change.
func (c *checker) hintRefs(res Result) (string, string) {
	return c.tool.VersionLink(version.RemovePrefix(res.CurrentDisplay)),
		c.tool.VersionLink(version.RemovePrefix(res.LatestDisplay))
}

// defaultCacheDir namespaces the cache file under the user cache directory
// ($XDG_CACHE_HOME or ~/.cache) by tool name.
func defaultCacheDir(name string) string {
	dir, err := shell.CacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, name)
}

// newer reports whether latest is a strictly greater release than current. A dev
// build sits ahead of its base tag, so it is compared on that base release:
// being a commit or two past the latest tag is not "behind" it. Unparseable
// versions yield false, so a malformed tag never nags.
func newer(current, latest string) bool {
	cur, err := version.Parse(current)
	if err != nil {
		return false
	}
	lat, err := version.Parse(latest)
	if err != nil {
		return false
	}
	if version.IsDev(current) {
		cur = cur.Core()
	}
	return version.GreaterThan(lat, cur)
}
