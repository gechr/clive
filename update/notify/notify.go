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
// that same run; otherwise its result appears on the next invocation. A tool
// describes itself with a [brew.Config] - the same value its self-update uses -
// calls [Check] before dispatching its command, and invokes the returned flush
// function after:
//
//	flush := notify.Check(brew.Config{
//		Info:    clive.Info{Module: "github.com/example/myapp"},
//		Formula: "myapp",
//	})
//	defer flush()
//
// The per-tool kill switch MYAPP_NO_UPDATE_CHECK (derived from the binary name)
// is a global opt-out: [Check] and [Pending] do not schedule a refresh and
// [Pending] reports no update. A non-terminal stderr only suppresses the
// auto-printed hint returned by [Check]; it does not gate [Pending].
//
// [Pending] exposes the same verdict without printing so hosts can render update
// state in their own UI. [Skip] records a dismissed ref in the same cache. The
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
	"github.com/gechr/clive/update/brew"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/gechr/x/terminal"
)

const (
	// cooldown is the minimum gap between network refreshes. The cache mtime
	// records the last refresh, so the network is hit at most once per cooldown.
	cooldown = 24 * time.Hour

	// lookupTimeout bounds the background tag fetch.
	lookupTimeout = 2 * time.Second

	// stampPerm is the mode of the cache file.
	stampPerm = 0o644

	// dirPerm is the mode of the per-tool cache directory.
	dirPerm = 0o755

	// stampName is the cache file under the per-tool cache directory. Its content
	// records the latest known ref and dismissed ref; its mtime is when that was
	// last refreshed.
	stampName = "last-update-check"

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
// never blocks and never touches the network on the calling path. cfg describes
// the tool: its binary name forms the kill-switch env var, the cache namespace,
// and the `<binary> update` command, and its display name opens the message.
//
// The returned function prints the hint; the caller invokes it after its command
// completes, so the hint follows the command's own output. It is a no-op when no
// update is pending, when the check is disabled, or when stderr is not a
// terminal. Non-terminal stderr suppresses only printing; stale caches may still
// refresh in the background.
func Check(cfg brew.Config, opts ...Option) func() {
	c := newChecker(cfg, opts...)

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
	// Track is the active release track name.
	Track string
	// CurrentDisplay is CurrentRef after [WithRefDisplay].
	CurrentDisplay string
	// LatestDisplay is LatestRef after [WithRefDisplay].
	LatestDisplay string
}

// Pending returns the cached update verdict without printing. The bool reports
// whether an update is pending after the comparator, track, cache, and dismissal
// state are applied. It uses the same stale-cache background refresh path as
// [Check], but unlike the auto-printed hint it does not require a TTY.
func Pending(cfg brew.Config, opts ...Option) (Result, bool) {
	c := newChecker(cfg, opts...)
	if os.Getenv(c.envVar()) != "" {
		return Result{}, false
	}
	c.scheduleRefresh()
	return c.pending()
}

// Skip dismisses ref on the active track until the latest known ref changes.
// The dismissal is persisted in the notify cache and survives process restarts.
func Skip(cfg brew.Config, ref string, opts ...Option) error {
	c := newChecker(cfg, opts...)
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

// Option configures a [Check]. The defaults target a real run; the seams exist
// for testing and for callers that supply their own HTTP client or accent.
type Option func(*checker)

// WithCurrentVersion overrides the running ref compared against the latest ref.
// It defaults to [clive.Current].
func WithCurrentVersion(v string) Option {
	return func(c *checker) { c.current = v }
}

// WithTransport sets the HTTP transport, the seam tests use to serve canned tag
// payloads without touching the network.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *checker) { c.client.Transport = rt }
}

// WithCacheDir overrides the directory holding the cache file.
func WithCacheDir(dir string) Option {
	return func(c *checker) { c.cacheDir = dir }
}

// WithColor overrides the accent colour of the update message. It defaults to
// the orange lipgloss.Color(updateColor).
func WithColor(c color.Color) Option {
	return func(ck *checker) { ck.color = c }
}

// WithComparator overrides the decision that latest is an update over current.
// The default comparator is semver-based and treats dev builds as ahead of their
// base tag. Non-semver tracks can provide a comparator such as current != latest.
func WithComparator(fn func(current, latest string) bool) Option {
	return func(c *checker) {
		if fn != nil {
			c.comparator = fn
		}
	}
}

// WithRefDisplay overrides how refs render in the auto-printed hint and in
// [Result] display fields. The default display is the raw ref unchanged.
func WithRefDisplay(fn func(string) string) Option {
	return func(c *checker) {
		if fn != nil {
			c.display = fn
		}
	}
}

// WithChannel selects a release track and namespaces the cache for that track.
// Switching tracks never compares the new track against another track's cached
// ref; the new track starts stale and refreshes independently.
func WithChannel(name string) Option {
	return func(c *checker) { c.channel = name }
}

// LatestFunc reports the latest released ref of the tool, such as "v1.2.3" or a
// commit hash. It is called only on a stale-cache refresh, off the calling path,
// and bounded by the same lookupTimeout as the default check.
type LatestFunc func(ctx context.Context) (string, error)

// WithLatestFunc overrides how a refresh discovers the latest ref. By default
// the latest ref is the highest semver tag in the tool's GitHub repository
// ([clive.Info.LatestTag]); a tool whose releases are not published as readable
// GitHub tags - e.g. a private repo distributed from an artifact bucket -
// supplies its own lookup here.
func WithLatestFunc(fn LatestFunc) Option {
	return func(c *checker) {
		if fn != nil {
			c.latest = fn
		}
	}
}

// checker holds the resolved configuration for one check.
type checker struct {
	cfg        brew.Config
	current    string
	channel    string
	client     *http.Client
	cacheDir   string
	color      color.Color
	latest     LatestFunc
	comparator func(current, latest string) bool
	display    func(string) string
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
func newChecker(cfg brew.Config, opts ...Option) *checker {
	c := &checker{
		cfg:        cfg,
		current:    clive.Current(),
		client:     &http.Client{Timeout: lookupTimeout},
		cacheDir:   defaultCacheDir(cfg.BinaryName()),
		color:      lipgloss.Color(updateColor),
		comparator: newer,
		display: func(s string) string {
			return s
		},
	}
	// Default to the GitHub-tags lookup, reading c.client at call time so a
	// WithTransport seam still applies; WithLatestFunc replaces it wholesale.
	c.latest = func(ctx context.Context) (string, error) {
		return c.cfg.Info.LatestTag(ctx, c.client)
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
		if res, ok := c.pending(); ok {
			c.printHint(res)
		}
	}
}

// scheduleRefresh starts a background refresh when the active track's cache is
// missing, stale, or belongs to another track.
func (c *checker) scheduleRefresh() {
	st, checkedAt, cached := c.readStamp()
	if !cached || st.Track != c.channel || time.Since(checkedAt) >= cooldown {
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
	if st.Latest == "" {
		return res, false
	}
	res.LatestIsUpdate = c.comparator(c.current, st.Latest)
	if st.Failed {
		return res, false
	}
	if st.Skipped != "" && st.Latest == st.Skipped {
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

// stampPath returns the active track's cache file.
func (c *checker) stampPath() string {
	if c.channel == "" {
		return filepath.Join(c.cacheDir, stampName)
	}
	return filepath.Join(c.cacheDir, stampName+"-"+url.PathEscape(c.channel))
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
	return strings.ToUpper(c.cfg.BinaryName()) + envSuffix
}

// printHint logs the one-line "update available" hint: a leading blank line, the
// 💡 symbol, the installed and latest refs as fields, and a coloured message
// whose `<binary> update` command is bold.
func (c *checker) printHint(res Result) {
	display := c.cfg.DisplayName()
	command := c.cfg.BinaryName() + " update"

	msg := display + " is outdated! Run '" + command + "' to upgrade"
	if !clog.ColorsDisabled() {
		style := lipgloss.NewStyle().Foreground(c.color)
		msg = style.Render(display+" is outdated! Run '") +
			style.Bold(true).Render(command) +
			style.Render("' to upgrade")
	}

	installed, latest := c.hintRefs(res)

	fmt.Fprintln(os.Stderr)
	clog.Warn().
		Symbol("💡").
		Str("installed", installed).
		Str("latest", latest).
		Msg(msg)
}

// hintRefs returns the rendered installed/latest fields used by printHint.
func (c *checker) hintRefs(res Result) (string, string) {
	return c.cfg.Info.VersionLink(res.CurrentDisplay),
		c.cfg.Info.VersionLink(res.LatestDisplay)
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
