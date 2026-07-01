package goinstall

// Option customises a [Config] built by [New].
type Option func(*Config)

// WithBinary sets the executable name written to GOBIN and used to locate the
// result for version reporting; it defaults to the last element of the module
// path.
func WithBinary(binary string) Option { return func(c *Config) { c.binary = binary } }

// WithBranch sets the ref the Dev channel installs from; it defaults to "main".
func WithBranch(branch string) Option { return func(c *Config) { c.branch = branch } }

// WithInstallDirectory sets the directory the binary is installed into, exported
// to the install as GOBIN so it lands on PATH rather than $GOPATH/bin. A leading
// "~" and any $ENV references are expanded; it defaults to ~/.local/bin.
func WithInstallDirectory(dir string) Option { return func(c *Config) { c.dir = dir } }

// WithName sets the human-facing display name shown in messages; it defaults to
// the binary (and thus module) name.
func WithName(name string) Option { return func(c *Config) { c.name = name } }

// WithNoProxy clears the proxy variables for the go subprocess, so an update
// bypasses an HTTP proxy that cannot reach the module's source.
func WithNoProxy() Option { return func(c *Config) { c.noProxy = true } }
