// Package notify performs a passive, never-blocking "you're behind" check for a
// Go CLI and prints a one-line hint when a newer GitHub release exists.
//
// The hint is served from a small cache file - an instant disk read, never the
// network - so it adds no latency to the command. When that cache is stale, a
// background goroutine refreshes it; the refresh is never awaited, so it
// overlaps the caller's work and is abandoned at process exit if still running.
// The flush re-reads the cache, so a refresh that finishes while the command
// runs is shown that same run; otherwise its result appears on the next
// invocation. A tool calls [Check] before dispatching its command and invokes
// the returned flush function after:
//
//	flush := notify.Check(clive.Info{Module: "github.com/me/myapp"}, "myapp")
//	defer flush()
//
// The check is silenced by the per-tool kill switch MYAPP_NO_UPDATE_CHECK
// (derived from the name) and by a non-terminal stderr, and the network is
// touched at most once per cooldown.
package notify

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gechr/clive"
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
	// is the latest known tag; its mtime is when that was last refreshed.
	stampName = "last-update-check"

	// envSuffix is appended to the upper-cased tool name to form the kill switch:
	// "myapp" becomes "MYAPP_NO_UPDATE_CHECK".
	envSuffix = "_NO_UPDATE_CHECK"
)

// Check serves an "update available" hint from the per-tool cache and, when that
// cache is stale, refreshes it in the background for the next invocation. It
// never blocks and never touches the network on the calling path. name is the
// binary/command, such as "myapp": it forms the kill-switch env var, the cache
// namespace, and the `<name> update` command shown in the hint.
//
// The returned function prints the hint; the caller invokes it after its command
// completes, so the hint follows the command's own output. It is a no-op when no
// update is pending, when the check is disabled, or when stderr is not a
// terminal.
func Check(info clive.Info, name string, opts ...Option) func() {
	c := newChecker(info, name, opts...)

	if os.Getenv(c.envVar()) != "" || !terminal.Is(os.Stderr) {
		return func() {}
	}

	return c.hint()
}

// Option configures a [Check]. The defaults target a real run; the seams exist
// for testing and for callers that supply their own HTTP client.
type Option func(*checker)

// WithCurrentVersion overrides the running version compared against the latest
// tag. It defaults to [clive.Current].
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

// checker holds the resolved configuration for one check.
type checker struct {
	info     clive.Info
	name     string
	current  string
	client   *http.Client
	cacheDir string
}

// newChecker builds a checker with real-run defaults, then applies opts.
func newChecker(info clive.Info, name string, opts ...Option) *checker {
	c := &checker{
		info:     info,
		name:     name,
		current:  clive.Current(),
		client:   &http.Client{Timeout: lookupTimeout},
		cacheDir: defaultCacheDir(name),
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
	_, checkedAt, cached := c.readStamp()

	if !cached || time.Since(checkedAt) >= cooldown {
		go c.refresh()
	}

	return func() {
		if latest, ok := c.shouldHint(); ok {
			printHint(c.info, c.name, c.current, latest)
		}
	}
}

// shouldHint reports the latest cached tag and whether it is newer than the
// running build. It re-reads the cache, so a background refresh that has since
// completed is taken into account.
func (c *checker) shouldHint() (string, bool) {
	latest, _, cached := c.readStamp()
	if !cached || c.current == "" {
		return "", false
	}
	return latest, newer(c.current, latest)
}

// refresh fetches the latest tag and rewrites the cache. On failure it re-stamps
// the prior value, so a failing check still respects the cooldown instead of
// refetching on every invocation. It is meant to run in its own goroutine.
func (c *checker) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	latest, err := c.info.LatestTag(ctx, c.client)
	if err != nil {
		clog.Debug().Err(err).Msg("Update check failed")
		prev, _, _ := c.readStamp()
		c.writeStamp(prev)
		return
	}
	c.writeStamp(latest)
}

// readStamp returns the cached latest tag and when it was written. cached is
// false on the first run or when no cache directory is available.
func (c *checker) readStamp() (string, time.Time, bool) {
	if c.cacheDir == "" {
		return "", time.Time{}, false
	}
	path := filepath.Join(c.cacheDir, stampName)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, false
	}
	return strings.TrimSpace(string(data)), info.ModTime(), true
}

// writeStamp records latest as the cache content, stamping the file's mtime to
// now. Failures are ignored: a check that cannot persist simply runs again next
// time, which is harmless.
func (c *checker) writeStamp(latest string) {
	if c.cacheDir == "" {
		return
	}
	if err := xos.EnsureDir(c.cacheDir, dirPerm); err != nil {
		return
	}
	_ = xos.AtomicWrite(filepath.Join(c.cacheDir, stampName), []byte(latest), stampPerm)
}

// envVar is the per-tool kill switch, such as "MYAPP_NO_UPDATE_CHECK".
func (c *checker) envVar() string {
	return strings.ToUpper(c.name) + envSuffix
}

// printHint logs the one-line "update available" hint.
func printHint(info clive.Info, name, current, latest string) {
	clog.Hint().
		Str("installed", info.VersionLink(current)).
		Str("latest", info.VersionLink(latest)).
		Msgf("A newer %s release is available; run `%s update`", name, name)
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
