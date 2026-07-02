package brew

import "time"

// Option customises a [Config] built by [New].
type Option func(*Config)

// WithBinary sets the executable name to clean up non-Homebrew copies of and to
// read the post-update version from; it defaults to the formula name.
func WithBinary(binary string) Option { return func(c *Config) { c.binary = binary } }

// WithFetchTimeout bounds the initial `brew update` formula refresh; the zero
// value uses a two-minute default. Raise it on a slow link, or lower it to fail
// faster.
func WithFetchTimeout(d time.Duration) Option { return func(c *Config) { c.fetchTimeout = d } }

// WithFormula overrides the Homebrew formula name, which otherwise defaults to
// the last element of the module path.
func WithFormula(formula string) Option { return func(c *Config) { c.formula = formula } }

// WithName sets the human-facing display name shown in messages, e.g. "NGINX"
// for the nginx formula; it defaults to the binary (and thus formula) name.
func WithName(name string) Option { return func(c *Config) { c.name = name } }

// WithNoProxy clears the proxy variables for the brew subprocesses, so an update
// bypasses a proxy that cannot reach Homebrew or the formula's source.
func WithNoProxy() Option { return func(c *Config) { c.noProxy = true } }

// WithOnConflict sets how non-Homebrew copies of the binary on PATH are handled;
// the default warns that each one may shadow the brew install.
func WithOnConflict(policy ConflictPolicy) Option {
	return func(c *Config) { c.onConflict = policy }
}

// WithRemoveTaps lists Homebrew taps to untap before installing, so a formula
// that has moved to a new tap is not resolved from a stale one. Best-effort.
func WithRemoveTaps(taps ...string) Option { return func(c *Config) { c.removeTaps = taps } }

// WithTap sets the "owner/name" tap hosting the formula; empty means a core
// formula.
func WithTap(tap string) Option { return func(c *Config) { c.tap = tap } }

// WithTapURL sets the git remote for the tap, needed for a private tap brew
// cannot resolve by name; empty lets brew resolve a public tap.
func WithTapURL(url string) Option { return func(c *Config) { c.tapURL = url } }

// WithResolveVersionFunc sets how the Homebrew-managed binary's version is
// read; the zero value runs `<binary> version`.
func WithResolveVersionFunc(fn ResolveVersionFunc) Option {
	return func(c *Config) { c.versionResolver = fn }
}
