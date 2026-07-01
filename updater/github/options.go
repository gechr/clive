package github

// Option customises a [Config] built by [New].
type Option func(*Config)

// WithBinary sets the executable name located inside a downloaded archive and
// installed into the install directory; it defaults to the repo (or module)
// name.
func WithBinary(binary string) Option { return func(c *Config) { c.binary = binary } }

// WithChecksumFile sets the name of the combined checksums asset verified against
// the download, for a release that names it other than goreleaser's default
// "checksums.txt". Ignored when checksum verification is skipped.
func WithChecksumFile(name string) Option { return func(c *Config) { c.checksumFile = name } }

// WithEnterpriseURL sets the API base URL of a GitHub Enterprise instance, e.g.
// "https://ghe.example.com/api/v3/"; releases and the gh token are then resolved
// for that host rather than github.com.
func WithEnterpriseURL(url string) Option { return func(c *Config) { c.enterpriseURL = url } }

// WithFilters sets regexps matched against asset names, to disambiguate a release
// that publishes several assets for the same OS/arch. An asset must match one of
// them in addition to the OS/arch/extension matching.
func WithFilters(filters ...string) Option { return func(c *Config) { c.filters = filters } }

// WithInstallDirectory sets the directory the binary is installed into. A leading
// "~" and any $ENV references are expanded; it defaults to ~/.local/bin.
func WithInstallDirectory(dir string) Option { return func(c *Config) { c.dir = dir } }

// WithName sets the human-facing display name shown in messages; it defaults to
// the binary (and thus repo) name.
func WithName(name string) Option { return func(c *Config) { c.name = name } }

// WithPrerelease makes the default channel (used by Check and the notify
// integration) consider prereleases.
func WithPrerelease() Option { return func(c *Config) { c.prerelease = true } }

// WithSkipChecksum disables sha256 verification of the downloaded asset against a
// published checksums file. Verification is on by default.
func WithSkipChecksum() Option { return func(c *Config) { c.skipChecksum = true } }

// WithTokenEnv names an environment variable consulted before the gh CLI's
// stored credentials for a GitHub token; it defaults to "GITHUB_TOKEN".
func WithTokenEnv(env string) Option { return func(c *Config) { c.tokenEnv = env } }

// WithUniversalArch sets the architecture name of a macOS universal binary asset
// (e.g. "all"); that asset is chosen when none matches the host's specific arch.
func WithUniversalArch(arch string) Option { return func(c *Config) { c.universalArch = arch } }
