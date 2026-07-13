package notify

import (
	"context"
	"image/color"
	"net/http"
	"time"

	"github.com/gechr/clive/updater"
)

// Option configures a [Check]. The defaults target a real run; the seams exist
// for testing and for callers that supply their own HTTP client or accent.
type Option func(*checker)

// LatestFunc reports the latest released ref of the tool, such as "v1.2.3" or a
// commit hash. It is called only on a stale-cache refresh, off the calling path,
// and bounded by the same lookupTimeout as the default check.
type LatestFunc func(ctx context.Context) (string, error)

// WithCacheDir overrides the directory holding the cache file.
func WithCacheDir(dir string) Option {
	return func(c *checker) { c.cacheDir = dir }
}

// WithStampDir overrides the subdirectory of the cache dir holding the stamp
// files (default "last-update"). An empty dir places them in the cache dir
// itself.
func WithStampDir(dir string) Option {
	return func(c *checker) { c.stampDir = dir }
}

// WithStampNames overrides the refresh and notify stamp file names (defaults
// "check" and "notify"). An empty name keeps its default.
func WithStampNames(refresh, notify string) Option {
	return func(c *checker) {
		if refresh != "" {
			c.refreshStamp = refresh
		}
		if notify != "" {
			c.notifyStamp = notify
		}
	}
}

// WithChannel selects a release track and namespaces the cache for that track.
// Switching tracks never compares the new track against another track's cached
// ref; the new track starts stale and refreshes independently.
func WithChannel(name string) Option {
	return func(c *checker) { c.channel = name }
}

// WithColor overrides the accent colour of the update message. It defaults to
// the orange lipgloss.Color(updateColor).
func WithColor(c color.Color) Option {
	return func(ck *checker) { ck.color = c }
}

// WithOutdatedHintSymbol overrides the glyph on the update hint (default 💡).
func WithOutdatedHintSymbol(symbol string) Option {
	return func(c *checker) {
		c.hintOpts = append(c.hintOpts, updater.WithOutdatedHintSymbol(symbol))
	}
}

// WithOutdatedHintCommand overrides the command the update hint tells the user
// to run (default "<binary> update"). Use this for a tool whose self-update is
// invoked differently, e.g. a flag-only grammar's "<binary> --self-update".
func WithOutdatedHintCommand(command string) Option {
	return func(c *checker) {
		c.hintOpts = append(c.hintOpts, updater.WithOutdatedHintCommand(command))
	}
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

// WithCurrentVersion overrides the running ref compared against the latest ref.
// It defaults to [clive.Current].
func WithCurrentVersion(v string) Option {
	return func(c *checker) { c.current = v }
}

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

// WithNotifyInterval overrides the minimum gap between repeat auto-printed hints,
// so a pending update is surfaced at most once per interval rather than on every
// invocation. It defaults to 24h. A non-positive interval prints on every run.
// It throttles only the hint returned by [Check]; [Pending] always reports the
// current verdict.
func WithNotifyInterval(d time.Duration) Option {
	return func(c *checker) { c.notifyInterval = d }
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

// WithRefreshInterval overrides the minimum gap between background network
// refreshes. It defaults to 24h. A non-positive interval refreshes on every
// invocation. This is independent of [WithNotifyInterval]: one governs how often
// the latest ref is re-fetched, the other how often a pending update is printed.
func WithRefreshInterval(d time.Duration) Option {
	return func(c *checker) { c.refreshInterval = d }
}

// WithTransport sets the HTTP transport, the seam tests use to serve canned tag
// payloads without touching the network.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *checker) { c.client.Transport = rt }
}
