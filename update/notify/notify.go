// Package notify performs a passive, rate-limited "you're behind" check for a
// Go CLI and prints a one-line hint when a newer GitHub release exists. It is
// advisory only: every failure path (no network, throttled, unparseable
// version) is swallowed so the check never disrupts the command the user ran.
//
// A tool calls [Check] once after a command completes:
//
//	notify.Check(ctx, clive.Info{Module: "github.com/me/myapp"}, "myapp")
//
// The check is silenced by the per-tool kill switch MYAPP_NO_UPDATE_CHECK
// (derived from the name), and at most one network request is made per cooldown,
// tracked by a stamp file under the user cache directory.
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
)

const (
	// cooldown is the minimum gap between checks. Each check touches a stamp file,
	// so however often the CLI runs, the network is hit at most once per cooldown.
	cooldown = 24 * time.Hour

	// lookupTimeout bounds the tag fetch so a slow network never delays the CLI.
	lookupTimeout = 2 * time.Second

	// stampPerm is the mode of the cooldown stamp file.
	stampPerm = 0o644

	// dirPerm is the mode of the per-tool cache directory.
	dirPerm = 0o755

	// stampName is the cooldown marker stored under the per-tool cache directory.
	stampName = "last-update-check"

	// envSuffix is appended to the upper-cased tool name to form the kill switch:
	// "myapp" becomes "MYAPP_NO_UPDATE_CHECK".
	envSuffix = "_NO_UPDATE_CHECK"
)

// Check runs a best-effort, rate-limited check for a newer GitHub release of
// info's repository and prints a hint when the running binary is behind. name is
// the binary/command, such as "myapp": it forms the kill-switch env var, the
// cache namespace, and the `<name> update` command shown in the hint. All
// failures are silent; a debug log records why a check was skipped.
func Check(ctx context.Context, info clive.Info, name string, opts ...Option) {
	c := newChecker(info, name, opts...)

	latest, ok := c.evaluate(ctx)
	if !ok {
		return
	}

	clog.Hint().
		Str("installed", c.info.VersionLink(c.current)).
		Str("latest", c.info.VersionLink(latest)).
		Msgf("A newer %s release is available; run `%s update`", c.name, c.name)
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

// WithCacheDir overrides the directory holding the cooldown stamp file. An empty
// dir disables the cooldown (every call checks), which tests rely on.
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

// evaluate reports the latest version and whether to hint about it. It returns
// ok=false - silently - when the check is disabled, still in cooldown, fails, or
// finds nothing newer.
func (c *checker) evaluate(ctx context.Context) (string, bool) {
	if os.Getenv(c.envVar()) != "" {
		return "", false
	}
	if !c.due() {
		return "", false
	}

	// Stamp before fetching so a failing check still starts the cooldown, rather
	// than retrying the network on every invocation.
	c.stamp()

	if c.current == "" {
		return "", false // a `go run` build has no version to compare
	}

	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	latest, err := c.info.LatestTag(ctx, c.client)
	if err != nil {
		clog.Debug().Err(err).Msg("Update check failed")
		return "", false
	}
	if latest == "" || !newer(c.current, latest) {
		return "", false
	}
	return latest, true
}

// envVar is the per-tool kill switch, such as "MYAPP_NO_UPDATE_CHECK".
func (c *checker) envVar() string {
	return strings.ToUpper(c.name) + envSuffix
}

// due reports whether enough time has elapsed since the last check. A missing or
// unreadable stamp (first run, no cache dir) is treated as due.
func (c *checker) due() bool {
	if c.cacheDir == "" {
		return true
	}
	info, err := os.Stat(filepath.Join(c.cacheDir, stampName))
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) >= cooldown
}

// stamp records "checked now" by (re)writing the stamp file, whose mtime the
// next due check reads. Failures are ignored: a check that cannot persist its
// cooldown simply runs again next time, which is harmless.
func (c *checker) stamp() {
	if c.cacheDir == "" {
		return
	}
	if err := xos.EnsureDir(c.cacheDir, dirPerm); err != nil {
		return
	}
	_ = xos.AtomicWrite(filepath.Join(c.cacheDir, stampName), nil, stampPerm)
}

// defaultCacheDir namespaces the stamp file under the user cache directory
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
