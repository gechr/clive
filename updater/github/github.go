// Package github self-updates a Go CLI binary from its GitHub releases. A tool
// describes itself with a [Config] and calls [Update], which resolves the latest
// release, picks the asset matching the host OS/arch, downloads and extracts the
// binary, and installs it into a directory (default ~/.local/bin). [Check]
// reports whether a newer release exists without installing anything.
//
// The release discovery, OS/arch asset matching, archive extraction, checksum
// validation, and rollback-safe replacement are done by
// github.com/creativeprojects/go-selfupdate; this package is a thin wrapper that
// gives a github-distributed tool the same Config/[updater.Tool] interface,
// clog UX, install-directory default, and notify integration as updater/brew and
// updater/goinstall. Private repositories work by piggybacking on the gh CLI's
// stored credentials (github.com/cli/go-gh) for the API token.
package github

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	ghauth "github.com/cli/go-gh/v2/pkg/auth"
	selfupdate "github.com/creativeprojects/go-selfupdate"
	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clive/version"
	xos "github.com/gechr/x/os"
)

const (
	// updateTimeout bounds a full update; a large asset over a slow link can be slow.
	updateTimeout = 5 * time.Minute
	// host is the GitHub host whose gh credentials are used.
	host = "github.com"
	// defaultTokenEnv is the token env var consulted when Config.TokenEnv is unset.
	defaultTokenEnv = "GITHUB_TOKEN" //nolint:gosec // G101: an env var name, not a credential
	// checksumsFilename is goreleaser's combined checksums asset, validated by default.
	checksumsFilename = "checksums.txt"
	// dirPerm is the mode of the install directory when it must be created.
	dirPerm = 0o755
	// binPerm is the mode of the installed binary (and the fresh-install placeholder).
	binPerm = 0o755
)

// Config satisfies the metadata interface notify consumes.
var _ updater.Tool = Config{}

// currentVersion reports the running binary's version. It is a package var so
// tests can pin a known value; production always uses [clive.Current].
var currentVersion = clive.Current

// resolve discovers the latest release via go-selfupdate, returning the updater
// for a follow-up install. It is a package var so tests can stub discovery
// without touching the network.
var resolve = func(ctx context.Context, cfg Config, prerelease bool) (*selfupdate.Updater, *selfupdate.Release, bool, error) {
	owner, name, err := repo(cfg.Info)
	if err != nil {
		return nil, nil, false, err
	}
	up, err := newUpdater(cfg, prerelease)
	if err != nil {
		return nil, nil, false, err
	}
	rel, found, err := up.DetectLatest(ctx, selfupdate.NewRepositorySlug(owner, name))
	return up, rel, found, err
}

// Channel selects which release [Update] installs.
type Channel int

const (
	// Latest installs the newest published, non-prerelease release; the default.
	Latest Channel = iota
	// Prerelease installs the newest release including prereleases.
	Prerelease
)

// ChannelFor maps a --pre flag to a Channel; unset is Latest.
func ChannelFor(prerelease bool) Channel {
	if prerelease {
		return Prerelease
	}
	return Latest
}

// Config identifies the tool for a GitHub release-asset self-update. Either
// Info.Repo or a github.com Info.Module is required to locate releases;
// everything else has a sensible default.
type Config struct {
	// Info carries the repo ("owner/name", via Info.Repo or a github.com
	// Info.Module) used to query releases and build version links.
	Info clive.Info
	// Name is the display name shown in messages. Defaults to the binary name.
	Name string
	// Binary is the executable name located inside a downloaded archive and
	// installed into Dir. Defaults to the repo (or module) name.
	Binary string
	// Dir is the directory the binary is installed into. A leading "~" and any
	// $ENV references are expanded. Defaults to ~/.local/bin.
	Dir string
	// Filters are optional regexp matched against asset names, to disambiguate a
	// release that publishes several assets for the same OS/arch. An asset must
	// match one of them in addition to the OS/arch/extension matching.
	Filters []string
	// TokenEnv names an environment variable consulted before the gh CLI's stored
	// credentials for a GitHub token. Defaults to "GITHUB_TOKEN".
	TokenEnv string
	// Prerelease makes the default channel (used by Check and the notify
	// integration) consider prereleases.
	Prerelease bool
	// SkipChecksum disables sha256 verification of the downloaded asset against a
	// published checksums.txt. Verification is on by default.
	SkipChecksum bool
}

// Check reports whether a newer release of cfg is available, without installing.
// It queries the GitHub releases API directly rather than the module proxy, so a
// binary distributed only as a release asset - or from a private repo - can call
// it without a Go toolchain on PATH.
func Check(ctx context.Context, cfg Config) error {
	_, rel, found, err := resolve(ctx, cfg, cfg.Prerelease)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	current := currentVersion()
	if !found || !isNewer(current, rel.Version()) {
		updater.UpToDate(cfg.DisplayName(), cfg.Info, current)
		return nil
	}
	updater.HintFor(cfg, current, rel.Version())
	return nil
}

// Update installs the latest cfg from its GitHub releases on the given channel.
func Update(ctx context.Context, cfg Config, channel Channel) error {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	prerelease := cfg.Prerelease || channel == Prerelease

	var (
		up    *selfupdate.Updater
		rel   *selfupdate.Release
		found bool
	)
	err := updater.Spin(ctx, fmt.Sprintf("Fetching latest %s release", cfg.DisplayName()),
		func(ctx context.Context) error {
			var derr error
			up, rel, found, derr = resolve(ctx, cfg, prerelease)
			return derr
		})
	if err != nil {
		return err
	}

	current := currentVersion()
	if !found || !isNewer(current, rel.Version()) {
		updater.UpToDate(cfg.DisplayName(), cfg.Info, current)
		return nil
	}

	dst, err := installPath(cfg)
	if err != nil {
		return err
	}
	if err = updater.Spin(ctx, fmt.Sprintf("Installing %s", cfg.DisplayName()),
		func(ctx context.Context) error {
			return up.UpdateTo(ctx, rel, dst)
		},
		updater.Field{Key: "version", Val: version.RemovePrefix(rel.Version())}); err != nil {
		return fmt.Errorf("updating %s: %w", cfg.DisplayName(), err)
	}

	updater.Report(cfg.DisplayName(), cfg.Info, current, rel.Version())
	return nil
}

// newUpdater builds the go-selfupdate updater: a GitHub source authenticated with
// the resolved token, a goreleaser checksums validator unless disabled, and any
// asset filters.
func newUpdater(cfg Config, prerelease bool) (*selfupdate.Updater, error) {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{APIToken: resolveToken(cfg)})
	if err != nil {
		return nil, fmt.Errorf("github: build source: %w", err)
	}
	var validator selfupdate.Validator
	if !cfg.SkipChecksum {
		validator = &selfupdate.ChecksumValidator{UniqueFilename: checksumsFilename}
	}
	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:     source,
		Validator:  validator,
		Filters:    cfg.Filters,
		Prerelease: prerelease,
	})
	if err != nil {
		return nil, fmt.Errorf("github: build updater: %w", err)
	}
	return up, nil
}

// resolveToken resolves a GitHub token, first non-empty wins: the configured env
// var, then the gh CLI's stored credentials. An empty result means anonymous
// access, which still reads public repositories.
func resolveToken(cfg Config) string {
	gh, _ := ghauth.TokenForHost(host)
	return cmp.Or(os.Getenv(cmp.Or(cfg.TokenEnv, defaultTokenEnv)), gh)
}

// installPath is the absolute path the binary is installed to, creating the
// install directory and, on a fresh install, an empty placeholder binary.
// go-selfupdate renames the existing binary to a .old backup before swapping in
// the new one (for rollback); on a first install the target does not exist yet,
// so the placeholder gives that step something to rename. The backup is removed
// on success.
func installPath(cfg Config) (string, error) {
	dir := updater.InstallDir(cfg.Dir)
	if dir == "" {
		return "", fmt.Errorf(
			"updating %s: cannot resolve an install directory; set Config.Dir", cfg.DisplayName(),
		)
	}
	if err := xos.EnsureDir(dir, dirPerm); err != nil {
		return "", fmt.Errorf("github: create %s: %w", dir, err)
	}
	dst := filepath.Join(dir, cfg.BinaryName())
	if err := ensureFile(dst); err != nil {
		return "", fmt.Errorf("github: prepare %s: %w", dst, err)
	}
	return dst, nil
}

// ensureFile creates path as an empty executable if it does not already exist.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, binPerm)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	return f.Close()
}

// isNewer reports whether latest is a strictly greater semver than current,
// returning false when either string does not parse. It uses clive's version
// rules (dev-build aware) so the gate matches brew and goinstall.
func isNewer(current, latest string) bool {
	cur, err := version.Parse(current)
	if err != nil {
		return false
	}
	lat, err := version.Parse(latest)
	if err != nil {
		return false
	}
	return version.GreaterThan(lat, cur)
}

// repo resolves the "owner", "name" pair from Info: Info.Repo when set, else a
// github.com Info.Module. A module major-version suffix (".../v2") is dropped.
func repo(info clive.Info) (string, string, error) {
	r := info.Repo
	if r == "" {
		if rest, ok := strings.CutPrefix(info.Module, "github.com/"); ok {
			r = rest
		}
	}
	owner, rest, found := strings.Cut(r, "/")
	if !found || owner == "" || rest == "" {
		return "", "", fmt.Errorf(
			"github: needs a GitHub repo; set Info.Repo or a github.com module",
		)
	}
	name, _, _ := strings.Cut(rest, "/")
	return owner, name, nil
}

// BinaryName is the executable/command name, defaulting to the repo (or module)
// name. It is also the name go-selfupdate looks for inside a downloaded archive.
func (c Config) BinaryName() string {
	if c.Binary != "" {
		return c.Binary
	}
	if _, name, err := repo(c.Info); err == nil {
		return name
	}
	if c.Info.Module != "" {
		return path.Base(c.Info.Module)
	}
	return ""
}

// DisplayName is the human-facing name used in messages, defaulting to the binary
// (and thus the repo) name when Name is unset.
func (c Config) DisplayName() string { return updater.DisplayName(c.Name, c.BinaryName()) }

// VersionLink renders v as a clickable link to its release or commit, delegating
// to the embedded [clive.Info]. It lets [Config] satisfy [updater.Tool].
func (c Config) VersionLink(v string) string { return c.Info.VersionLink(v) }

// LatestRef returns the latest release's tag, letting [Config] satisfy
// [updater.Tool] so a github-distributed tool feeds notify the same "latest" the
// updater installs. The client is unused: go-selfupdate manages its own HTTP.
func (c Config) LatestRef(ctx context.Context, _ *http.Client) (string, error) {
	_, rel, found, err := resolve(ctx, c, c.Prerelease)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return rel.Version(), nil
}
