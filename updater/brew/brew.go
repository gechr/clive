// Package brew self-updates a Go CLI binary through Homebrew. A tool describes
// itself with a [Config] and calls [Update]; it refreshes the formula, then
// upgrades (or taps and installs), with a stable and a dev (--HEAD) channel, and
// trashes stray non-Homebrew copies so the brew install is authoritative.
// [Check] reports whether a newer release exists without installing anything.
//
// It is one update mechanism under clive/updater; others (goinstall, github) sit
// alongside it and share that package's UX helpers. The clog dependency lives
// here, keeping the core clive package dependency-light for version-only
// consumers.
package brew

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
	xos "github.com/gechr/x/os"
)

// brewTimeout bounds a full update; compiling --HEAD from source can be slow.
const brewTimeout = 5 * time.Minute

// headBuild matches a dev/HEAD version like "0.1.0-gabc1234-dev", so an upgrade
// of a source build re-fetches HEAD rather than dropping to a stable release.
var headBuild = regexp.MustCompile(`-g?[0-9a-f]{7,}-dev$`)

// Channel selects what [Config.Run] installs.
type Channel int

const (
	// Upgrade upgrades an installed formula to its latest version (the default).
	Upgrade Channel = iota
	// Stable installs the latest stable release, replacing any dev build.
	Stable
	// Dev builds and installs the latest source (HEAD).
	Dev
)

// ConflictPolicy decides what an update does with copies of the binary found on
// PATH outside Homebrew, which would otherwise shadow the brew install.
type ConflictPolicy int

const (
	// ConflictWarn leaves stray non-Homebrew copies in place but warns about each
	// one. It is the zero value, and thus the default.
	ConflictWarn ConflictPolicy = iota
	// ConflictUninstall trashes stray copies (recoverable, falling back to a
	// permanent remove where the platform cannot trash) so the brew install is
	// authoritative.
	ConflictUninstall
	// ConflictIgnore leaves stray copies in place silently.
	ConflictIgnore
)

// Config satisfies the metadata interface notify consumes.
var _ updater.Tool = Config{}

// ChannelFor maps a --dev/--stable flag pair to a Channel; neither set is
// Upgrade.
func ChannelFor(dev, stable bool) Channel {
	switch {
	case dev:
		return Dev
	case stable:
		return Stable
	default:
		return Upgrade
	}
}

// Config identifies the tool for a Homebrew self-update. Only Info, Name, and
// Formula are required; Tap/TapURL are needed for a formula outside homebrew/core.
type Config struct {
	// Info carries the module path and repo for version checks and release links.
	Info clive.Info
	// Name is the display name shown in messages, e.g. "NGINX" for the nginx
	// formula. Defaults to the binary name (and thus the formula) when unset.
	Name string
	// Formula is the Homebrew formula name.
	Formula string
	// Tap is the "owner/name" tap hosting the formula; empty means a core formula.
	Tap string
	// TapURL is the git remote for Tap, for a private tap brew cannot resolve by
	// name; empty lets brew resolve a public tap.
	TapURL string
	// Binary is the executable name to clean up non-brew copies of; defaults to
	// Formula.
	Binary string
	// OnConflict decides how non-Homebrew copies of the binary on PATH are
	// handled; the zero value warns that each one may shadow the brew install.
	OnConflict ConflictPolicy
	// NoProxy clears the proxy variables for the brew subprocesses, so an update
	// bypasses a proxy that cannot reach Homebrew or the formula's source.
	NoProxy bool
	// RemoveTaps lists Homebrew taps to untap before installing, so a formula
	// that has moved to a new tap is not resolved from a stale one. Best-effort.
	RemoveTaps []string
}

// Check reports whether a newer release of cfg is available, without
// installing.
func Check(ctx context.Context, cfg Config) error {
	available, err := cfg.Info.UpdateAvailable(ctx)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	if !available {
		updater.UpToDate(cfg.DisplayName(), cfg.Info, clive.Current())
		return nil
	}
	latest, _ := cfg.Info.Latest(ctx)
	updater.HintFor(cfg, clive.Current(), latest)
	return nil
}

// Update installs the latest cfg via Homebrew on the given channel.
func Update(ctx context.Context, cfg Config, channel Channel) error {
	ctx, cancel := context.WithTimeout(ctx, brewTimeout)
	defer cancel()

	brew, err := exec.LookPath("brew")
	if err != nil {
		return fmt.Errorf(
			"updating %s needs Homebrew; install it from https://brew.sh",
			cfg.DisplayName(),
		)
	}
	r := &runner{cfg: cfg, brew: brew, current: clive.Current()}

	if err := r.spin(
		ctx,
		fmt.Sprintf("Fetching latest %s Homebrew formula", cfg.DisplayName()),
		"update",
		"--quiet",
	); err != nil {
		return err
	}
	switch channel {
	case Stable:
		return r.reinstall(ctx, false)
	case Dev:
		return r.reinstall(ctx, true)
	case Upgrade:
		return r.upgrade(ctx)
	}
	return r.upgrade(ctx)
}

// runner holds the brew invocation state for one update.
type runner struct {
	cfg     Config
	brew    string
	current string
}

// upgrade upgrades an installed formula, tapping and installing it first when it
// is not yet present. A dev build re-fetches HEAD so it stays on source.
func (r *runner) upgrade(ctx context.Context) error {
	if !r.installed(ctx) {
		return r.install(ctx, headBuild.MatchString(r.current))
	}
	args := []string{"upgrade"}
	if headBuild.MatchString(r.current) {
		args = append(args, "--fetch-HEAD")
	}
	args = append(args, r.cfg.Formula)
	if err := r.spin(ctx, fmt.Sprintf("Upgrading %s", r.cfg.DisplayName()), args...); err != nil {
		return err
	}
	r.cleanup(ctx)
	return r.report(ctx)
}

// reinstall uninstalls any existing copy then installs the chosen channel,
// switching cleanly between stable and dev (--HEAD) builds.
func (r *runner) reinstall(ctx context.Context, head bool) error {
	r.brewSilent(ctx, "uninstall", "--ignore-dependencies", r.cfg.Formula)
	return r.install(ctx, head)
}

// install taps (when needed) and installs the formula, optionally from HEAD.
func (r *runner) install(ctx context.Context, head bool) error {
	r.removeTaps(ctx)
	if r.cfg.Tap != "" {
		if err := r.tap(ctx); err != nil {
			return err
		}
	}
	args := []string{"install"}
	if head {
		args = append(args, "--HEAD")
	}
	args = append(args, r.cfg.formulaRef())

	verb := "Installing"
	if head {
		verb = "Compiling"
	}
	if err := r.spin(ctx, fmt.Sprintf("%s %s", verb, r.cfg.DisplayName()), args...); err != nil {
		return err
	}
	r.cleanup(ctx)
	return r.report(ctx)
}

// removeTaps untaps each stale tap in cfg, best-effort, so a relocated formula
// is not resolved from an old tap. Errors (e.g. a tap not present) are ignored.
func (r *runner) removeTaps(ctx context.Context) {
	for _, t := range r.cfg.RemoveTaps {
		r.brewSilent(ctx, "untap", t)
	}
}

// tap registers the configured tap (with its git URL for a private tap) so the
// formula resolves. It runs silently (no spinner line) but still returns an
// error, so a genuine tap failure stops the update instead of being masked.
func (r *runner) tap(ctx context.Context) error {
	args := []string{"tap", r.cfg.Tap}
	if r.cfg.TapURL != "" {
		args = append(args, r.cfg.TapURL)
	}
	return r.run(ctx, args...)
}

// report logs the resulting version, as an old→new pair when it changed. It
// returns nil so callers can `return r.report(ctx)` in the success path.
func (r *runner) report(ctx context.Context) error {
	updater.Report(r.cfg.DisplayName(), r.cfg.Info, r.current, r.installedVersion(ctx))
	return nil
}

// installed reports whether brew already manages the formula.
func (r *runner) installed(ctx context.Context) bool {
	return r.brewCmd(ctx, "list", r.cfg.Formula).Run() == nil
}

// installedVersion returns the formula's active (linked) version via brew, or
// "". An upgrade links the freshly-installed keg, so the linked keg is the
// version now on PATH. This reads `brew info --json`, not `brew list --versions`:
// the latter enumerates every installed keg in arbitrary order, so a stale keg
// left behind before `brew cleanup` could be reported instead of the new one.
func (r *runner) installedVersion(ctx context.Context) string {
	out, err := r.brewCmd(ctx, "info", "--json=v2", r.cfg.Formula).Output()
	if err != nil {
		return ""
	}
	return linkedKeg(out)
}

// brewInfoJSON is the subset of `brew info --json=v2` that names the active keg.
type brewInfoJSON struct {
	Formulae []struct {
		LinkedKeg string `json:"linked_keg"`
	} `json:"formulae"`
}

// linkedKeg returns the active (linked) version from `brew info --json=v2`
// output, or "" when nothing is linked.
func linkedKeg(data []byte) string {
	var info brewInfoJSON
	if err := json.Unmarshal(data, &info); err != nil || len(info.Formulae) == 0 {
		return ""
	}
	return info.Formulae[0].LinkedKeg
}

// cleanup handles copies of the binary found on PATH outside Homebrew, so the
// brew install is the one that runs. Its action is governed by cfg.OnConflict.
// It is best-effort and never fails the update.
func (r *runner) cleanup(ctx context.Context) {
	if r.cfg.OnConflict == ConflictIgnore {
		return
	}
	out, err := r.brewCmd(ctx, "--prefix").Output()
	if err != nil {
		return
	}
	brewBin := strings.TrimSpace(string(out)) + "/bin"

	// A copy shadows the brew install only when its PATH directory comes before
	// Homebrew's bin: that is the one a name lookup resolves to. Tracking whether
	// brewBin has been seen yet, in PATH order, tells us which side a copy is on.
	seenBrewBin := false
	for dir := range strings.SplitSeq(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		if dir == brewBin {
			seenBrewBin = true
			continue
		}
		path := dir + string(os.PathSeparator) + r.cfg.BinaryName()
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		mode := info.Mode()
		executable := mode.IsRegular() && mode&0o111 != 0
		symlink := mode&os.ModeSymlink != 0
		if !executable && !symlink {
			continue
		}
		r.resolveConflict(path, !seenBrewBin)
	}
}

// resolveConflict applies cfg.OnConflict to a single non-Homebrew copy at path.
// shadows reports whether the copy precedes Homebrew on PATH, and so is the one a
// name lookup actually resolves to. A warn stays silent about a copy that does
// not shadow, since it is harmless; an uninstall trashes every stray copy.
func (r *runner) resolveConflict(path string, shadows bool) {
	if r.cfg.OnConflict == ConflictWarn {
		if shadows {
			clog.Warn().
				Path("path", path).
				Msgf("%s is shadowed by another copy earlier in your `$PATH`", r.cfg.DisplayName())
		}
		return
	}
	// Trash the stray copy so it can be recovered, falling back to a permanent
	// remove on a platform that cannot trash (e.g. macOS older than 15).
	switch err := xos.Trash(path); {
	case err == nil:
		clog.Info().
			Symbol("🗑️").
			Path("path", path).
			Msgf("Trashed stray %s installation", r.cfg.DisplayName())
	case errors.Is(err, errors.ErrUnsupported):
		r.removeConflict(path)
	default:
		clog.Warn().
			Path("path", path).
			Err(err).
			Msgf("Failed to trash stray %s installation", r.cfg.DisplayName())
	}
}

// removeConflict permanently removes a stray copy: the fallback for a platform
// that cannot move it to the trash.
func (r *runner) removeConflict(path string) {
	if err := os.Remove(path); err != nil {
		clog.Warn().
			Path("path", path).
			Err(err).
			Msgf("Failed to remove stray %s installation", r.cfg.DisplayName())
	} else {
		clog.Info().
			Symbol("🗑️").
			Path("path", path).
			Msgf("Removed stray %s installation", r.cfg.DisplayName())
	}
}

// spin runs a brew command under a spinner via [updater.Spin], logging a
// completion line on success and surfacing [runner.run]'s error on failure.
func (r *runner) spin(ctx context.Context, msg string, args ...string) error {
	return updater.Spin(ctx, msg, func(ctx context.Context) error {
		return r.run(ctx, args...)
	})
}

// run executes a brew command without any logging, capturing stderr so a
// failure carries brew's own message rather than a bare "exit status 1". In
// verbose mode the command's output is also streamed to the terminal.
func (r *runner) run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := r.brewCmd(ctx, args...)
	if clog.IsVerbose() {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return errors.New(detail)
		}
		return err
	}
	return nil
}

// brewSilent runs a best-effort brew command, ignoring its outcome (e.g. an
// uninstall of something not installed).
func (r *runner) brewSilent(ctx context.Context, args ...string) {
	_ = r.brewCmd(ctx, args...).Run()
}

// brewCmd builds a brew command with Homebrew's env-hint noise suppressed,
// clearing the proxy when cfg.NoProxy is set.
func (r *runner) brewCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.brew, args...) //nolint:gosec // controlled args
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1")
	if r.cfg.NoProxy {
		cmd.Env = append(cmd.Env, updater.ProxyBypass()...)
	}
	return cmd
}

// BinaryName is the executable/command name, defaulting to the formula. Shared
// by other update mechanisms (the periodic check) that name the `<binary>
// update` command.
func (c Config) BinaryName() string { return cmp.Or(c.Binary, c.Formula) }

// DisplayName is the human-facing name used in messages, defaulting to the
// binary (and thus the formula) name when Name is unset.
func (c Config) DisplayName() string { return updater.DisplayName(c.Name, c.BinaryName()) }

// VersionLink renders v as a clickable link to its release or commit, delegating
// to the embedded [clive.Info]. It lets [Config] satisfy [updater.Tool].
func (c Config) VersionLink(v string) string { return c.Info.VersionLink(v) }

// LatestRef returns the highest semver tag in the tool's repository, delegating
// to [clive.Info.LatestTag]. It lets [Config] satisfy [updater.Tool].
func (c Config) LatestRef(ctx context.Context, client *http.Client) (string, error) {
	return c.Info.LatestTag(ctx, client)
}

// formulaRef is the brew install target: tap-qualified when a tap is set.
func (c Config) formulaRef() string {
	if c.Tap != "" {
		return c.Tap + "/" + c.Formula
	}
	return c.Formula
}
