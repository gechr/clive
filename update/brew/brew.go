// Package brew self-updates a Go CLI binary through Homebrew. A tool describes
// itself with a [Config] and calls [Update]; it refreshes the formula, then
// upgrades (or taps and installs), with a stable and a dev (--HEAD) channel, and
// removes stray non-Homebrew copies so the brew install is authoritative.
// [Check] reports whether a newer release exists without installing anything.
//
// It is one update mechanism under clive/update; others (e.g. a release-asset
// downloader) can sit alongside it. The clog dependency lives here, keeping the
// core clive package dependency-light for version-only consumers.
package brew

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gechr/clive"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
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
	// KeepOtherInstalls leaves non-Homebrew copies of the binary in place;
	// otherwise they are removed so the brew install is authoritative.
	KeepOtherInstalls bool
}

// Check reports whether a newer release of cfg is available, without
// installing.
func Check(ctx context.Context, cfg Config) error {
	available, err := cfg.Info.UpdateAvailable(ctx)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	if !available {
		upToDate(cfg.DisplayName(), cfg.Info, clive.Current())
		return nil
	}
	latest, _ := cfg.Info.Latest(ctx)
	clog.Info().
		Str("current", cfg.Info.VersionLink(clive.Current())).
		Str("latest", cfg.Info.VersionLink(latest)).
		Msgf("An update is available; run `%s update`", cfg.DisplayName())
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

// report logs the resulting version, as an old→new pair when it changed.
func (r *runner) report(ctx context.Context) error {
	old := version.RemovePrefix(r.current)
	current := version.RemovePrefix(r.installedVersion(ctx))
	if old != "" && current != "" && old != current {
		clog.Info().
			Str("old", r.cfg.Info.VersionLink(old)).
			Str("new", r.cfg.Info.VersionLink(current)).
			Msgf("Updated %s", r.cfg.DisplayName())
		return nil
	}
	upToDate(r.cfg.DisplayName(), r.cfg.Info, cmp.Or(current, old))
	return nil
}

// upToDate warns that no update was applied, including the version field only
// when a version is known (a go-run build has none to show).
func upToDate(name string, info clive.Info, ver string) {
	e := clog.Warn()
	if ver != "" {
		e = e.Str("version", info.VersionLink(ver))
	}
	e.Msgf("%s is already up-to-date", name)
}

// installed reports whether brew already manages the formula.
func (r *runner) installed(ctx context.Context) bool {
	return r.brewCmd(ctx, "list", r.cfg.Formula).Run() == nil
}

// installedVersion returns the formula's installed version via brew, or "".
func (r *runner) installedVersion(ctx context.Context) string {
	out, err := r.brewCmd(ctx, "list", "--versions", r.cfg.Formula).Output()
	if err != nil {
		return ""
	}
	// "formula 1.2.3" -> "1.2.3".
	fields := strings.Fields(string(out))
	if len(fields) < 2 { //nolint:mnd // a formula listing is "name version"
		return ""
	}
	return fields[len(fields)-1]
}

// cleanup removes copies of the binary found on PATH outside Homebrew, so the
// brew install is the one that runs. It is best-effort and never fails the
// update.
func (r *runner) cleanup(ctx context.Context) {
	if r.cfg.KeepOtherInstalls {
		return
	}
	out, err := r.brewCmd(ctx, "--prefix").Output()
	if err != nil {
		return
	}
	brewBin := strings.TrimSpace(string(out)) + "/bin"

	for dir := range strings.SplitSeq(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if dir == "" || dir == brewBin {
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
		if err := os.Remove(path); err != nil {
			clog.Warn().
				Str("path", path).
				Err(err).
				Msgf("Could not remove a stray %s installation", r.cfg.DisplayName())
		} else {
			clog.Info().
				Str("path", path).
				Msgf("Removed a stray %s installation", r.cfg.DisplayName())
		}
	}
}

// spin runs a brew command under a spinner: it logs a completion line on
// success, and on failure returns the error (via [runner.run]) for the caller
// to surface. The failure path uses Silent so the spinner does not log its own
// error line, leaving the caller to report the failure exactly once.
func (r *runner) spin(ctx context.Context, msg string, args ...string) error {
	res := clog.Spinner(msg).Elapsed("elapsed").Wait(ctx, func(ctx context.Context) error {
		return r.run(ctx, args...)
	})
	if err := res.Silent(); err != nil {
		return err
	}
	return res.Msg(msg)
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

// brewCmd builds a brew command with Homebrew's env-hint noise suppressed.
func (r *runner) brewCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.brew, args...) //nolint:gosec // controlled args
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1")
	return cmd
}

// BinaryName is the executable/command name, defaulting to the formula. Shared
// by other update mechanisms (the periodic check) that name the `<binary>
// update` command.
func (c Config) BinaryName() string { return cmp.Or(c.Binary, c.Formula) }

// DisplayName is the human-facing name used in messages, defaulting to the
// binary (and thus the formula) name when Name is unset.
func (c Config) DisplayName() string { return cmp.Or(c.Name, c.BinaryName()) }

// formulaRef is the brew install target: tap-qualified when a tap is set.
func (c Config) formulaRef() string {
	if c.Tap != "" {
		return c.Tap + "/" + c.Formula
	}
	return c.Formula
}
