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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
	"github.com/gechr/clog/fx"
	xos "github.com/gechr/x/os"
	xstrings "github.com/gechr/x/strings"
)

// brewTimeout bounds a full update; compiling --HEAD from source can be slow.
const brewTimeout = 5 * time.Minute

// defaultFetchTimeout bounds the initial `brew update` formula refresh, which is
// normally quick. A much longer hang means a stuck network fetch, so we cut it
// off well before the overall brewTimeout rather than blocking the whole update.
// It also bounds how long fetch waits for a concurrent brew process to release
// the update lock. A consumer can override it per-update via [WithFetchTimeout].
const defaultFetchTimeout = 2 * time.Minute

// brewLockPoll is how often [runner.waitLock] re-checks whether a concurrent
// brew process has released a `brew update` or per-formula lock.
const brewLockPoll = time.Second

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

// Config satisfies the metadata interface notify consumes and the
// behavioural [updater.Updater] interface.
var _ updater.Updater = Config{}

// Check implements [updater.Updater].
func (c Config) Check(ctx context.Context) error { return Check(ctx, c) }

// Update implements [updater.Updater], mapping dev/stable onto [ChannelFor].
func (c Config) Update(ctx context.Context, dev, stable bool) error {
	return Update(ctx, c, ChannelFor(dev, stable))
}

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

// Config identifies the tool for a Homebrew self-update. Build it with [New]:
// only the module info is required - the formula name defaults to the module's
// last path element, overridden with [WithFormula] - and optional behaviour is
// set with the With* [Option]s. It satisfies [updater.Tool] for notify.
type Config struct {
	binary          string
	fetchTimeout    time.Duration
	formula         string
	info            clive.Info
	name            string
	noProxy         bool
	onConflict      ConflictPolicy
	removeTaps      []string
	tap             string
	tapURL          string
	versionResolver ResolveVersionFunc
}

// ResolveVersionFunc returns the version reported by the Homebrew-managed
// binary at bin.
type ResolveVersionFunc func(ctx context.Context, bin string) (string, error)

// New builds a [Config] for a Homebrew self-update. info carries the module and
// repo used for version checks and release links; the Homebrew formula name
// defaults to the last element of the module path and is overridden with
// [WithFormula]. Optional behaviour is configured with the With* [Option]s.
func New(info clive.Info, opts ...Option) Config {
	c := Config{info: info}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Check reports whether a newer release of cfg is available, without
// installing.
func Check(ctx context.Context, cfg Config) error {
	available, err := cfg.info.UpdateAvailable(ctx)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	if !available {
		updater.UpToDate(cfg.DisplayName(), cfg.info, clive.Current())
		return nil
	}
	latest, _ := cfg.info.Latest(ctx)
	updater.HintFor(cfg, clive.Current(), latest)
	return nil
}

// Update installs the latest cfg via Homebrew on the given channel.
func Update(ctx context.Context, cfg Config, channel Channel) error {
	ctx, cancel := context.WithTimeout(ctx, brewTimeout)
	defer cancel()

	if cfg.resolveFormula() == "" {
		return fmt.Errorf(
			"updating %s needs a formula; set a module or use brew.WithFormula",
			cfg.DisplayName(),
		)
	}
	brew, err := exec.LookPath("brew")
	if err != nil {
		return fmt.Errorf(
			"updating %s needs Homebrew; install it from https://brew.sh",
			cfg.DisplayName(),
		)
	}
	r := &runner{cfg: cfg, brew: brew, current: clive.Current()}

	// Probe the current install concurrently with the metadata fetch. The
	// probes are read-only (brew --prefix, brew list, the binary's own
	// version) but each costs a few hundred milliseconds; overlapping them
	// with the fetch removes the blank pause between the fetch spinner
	// completing and the next phase's first frame.
	//
	// Recording the Homebrew install's own version before we touch it lets
	// the report compare the brew binary against itself. clive.Current() is
	// the *running* binary, which - when a non-Homebrew copy shadows the brew
	// install on PATH - is a different install entirely, and comparing its
	// version against the freshly-installed brew binary yields a nonsensical
	// "downgrade". Empty when brew has nothing installed yet (a fresh
	// install), which reports no change.
	probed := make(chan struct{})
	go func() {
		defer close(probed)
		r.before = r.installedVersion(ctx)
		r.present = r.installed(ctx)
	}()

	if err := r.fetch(ctx); err != nil {
		return err
	}
	<-probed

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

// fetch refreshes the formula metadata via `brew update`. The refresh is
// normally quick, so it runs under fetchTimeout - far tighter than the overall
// brewTimeout that must also cover a --HEAD source compile - and on timeout
// supplants its spinner with a clear "Timed out ..." line rather than hanging
// or surfacing brew's opaque "signal: killed". When another brew process already
// holds the update lock, fetch waits it out (see [runner.update]) rather than
// failing, so the fetchTimeout also bounds that wait.
func (r *runner) fetch(ctx context.Context) error {
	name := r.cfg.DisplayName()
	return updater.SpinTimeoutProgress(
		ctx,
		fmt.Sprintf("Fetching latest %s Homebrew formula", name),
		fmt.Sprintf("Fetched latest %s Homebrew formula", name),
		fmt.Sprintf("Timed out while fetching %s Homebrew formula", name),
		cmp.Or(r.cfg.fetchTimeout, defaultFetchTimeout),
		func(ctx context.Context, u *fx.Update) error {
			return r.update(ctx, func() {
				u.Msgf("Waiting for another Homebrew process to finish").Send()
			})
		},
	)
}

// update runs `brew update`, tolerating a concurrent brew process that already
// holds the update lock. Homebrew serialises `brew update` with a non-blocking
// lock and exits immediately - "Another brew update process is already running"
// - when it is held. Because one `brew update` refreshes *every* formula, the
// running process is already doing our work, so rather than fail (or wastefully
// re-run it) we wait for that process to release the lock, leaving the metadata
// freshly updated for the upgrade that follows. onWait, invoked once when the
// wait begins, lets the caller note the pause (e.g. relabel a spinner).
func (r *runner) update(ctx context.Context, onWait func()) error {
	err := r.run(ctx, "update", "--quiet")
	if err == nil || !isBrewLocked(err) {
		return err
	}
	if onWait != nil {
		onWait()
	}
	return r.waitLock(ctx, r.updateLockPath(ctx))
}

// runAwaitingLock runs a mutating brew command that must actually execute (an
// upgrade or install, unlike the skippable update), waiting out a concurrent
// brew process that holds the formula's lock. Homebrew locks each formula with a
// non-blocking flock and fails fast - "A `brew ...` process has already locked
// ..." - rather than running two upgrades of one formula at once; so we hold off
// while the lock is taken and run only once it frees, by which point the other
// process has finished and our command is a clean no-op. onWait(true) is invoked
// when the wait begins and onWait(false) when it ends, letting the caller flip a
// spinner between "Waiting ..." and its active label so it reads "Waiting" only
// while actually blocked. The wait is bounded by ctx (the overall brewTimeout).
func (r *runner) runAwaitingLock(
	ctx context.Context,
	onWait func(waiting bool),
	args ...string,
) error {
	for {
		// Pre-check the formula lock so the spinner shows "Waiting ..." before we
		// invoke brew, rather than flashing the active label through brew's
		// fail-fast on a held lock.
		if path := r.formulaLockPath(ctx); path != "" && lockHeld(path) {
			if onWait != nil {
				onWait(true)
			}
			if err := r.waitLock(ctx, path); err != nil {
				return err
			}
			if onWait != nil {
				onWait(false)
			}
		}
		err := r.run(ctx, args...)
		if err == nil || !isBrewLocked(err) {
			return err
		}
		// The lock was taken between our pre-check and the run (or is a lock we
		// could not pre-check): throttle, then loop to wait it out afresh.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(brewLockPoll):
		}
	}
}

// waitRelabel returns a runAwaitingLock onWait callback that flips spinner u
// between a "Waiting ..." label while blocked and active once it proceeds, so the
// spinner reads "Waiting" only while it is genuinely held off.
func waitRelabel(u *fx.Update, active string) func(bool) {
	return func(waiting bool) {
		if waiting {
			u.Msg("Waiting for another Homebrew process to finish").Send()
			return
		}
		u.Msg(active).Send()
	}
}

// waitLock blocks until the Homebrew lock file at path is free or ctx is done,
// polling a non-blocking flock exactly as brew itself does. An empty path (the
// lock could not be located) returns immediately: a probe we cannot perform must
// not stall the update indefinitely.
func (r *runner) waitLock(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	for lockHeld(path) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(brewLockPoll):
		}
	}
	return nil
}

// updateLockPath is the file Homebrew flocks for the duration of a `brew update`
// (<prefix>/var/homebrew/locks/update; see utils/lock.sh), or "" when the prefix
// cannot be resolved.
func (r *runner) updateLockPath(ctx context.Context) string {
	return r.lockPath(ctx, "update")
}

// formulaLockPath is the per-formula file Homebrew flocks for the duration of an
// upgrade or install (<prefix>/var/homebrew/locks/<formula>.formula.lock; keyed
// by the unqualified formula/rack name), or "" when it cannot be resolved.
func (r *runner) formulaLockPath(ctx context.Context) string {
	formula := r.cfg.resolveFormula()
	if formula == "" {
		return ""
	}
	return r.lockPath(ctx, formula+".formula.lock")
}

// lockPath resolves a file within Homebrew's locks directory, or "" when the
// prefix cannot be determined.
func (r *runner) lockPath(ctx context.Context, name string) string {
	prefix := r.brewPrefix(ctx)
	if prefix == "" {
		return ""
	}
	return prefix + "/var/homebrew/locks/" + name
}

// isBrewLocked reports whether err is Homebrew refusing to run because another
// brew process already holds the relevant lock: the global update lock ("already
// running"), or a per-formula upgrade/install lock ("has already locked ...").
func isBrewLocked(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already running") || strings.Contains(msg, "already locked")
}

// runner holds the brew invocation state for one update.
type runner struct {
	// mu guards prefix, memoized lazily and read from both the fetch path (to
	// locate the update lock) and the concurrent install probe.
	mu      sync.Mutex
	before  string
	brew    string
	cfg     Config
	current string
	prefix  string
	present bool
}

// upgrade upgrades an installed formula, tapping and installing it first when it
// is not yet present. A dev build re-fetches HEAD so it stays on source.
func (r *runner) upgrade(ctx context.Context) error {
	if !r.present {
		return r.install(ctx, headBuild.MatchString(r.current))
	}
	args := []string{"upgrade"}
	if headBuild.MatchString(r.current) {
		args = append(args, "--fetch-HEAD")
	}
	args = append(args, r.cfg.formulaRef())
	msg := fmt.Sprintf("Upgrading %s", r.cfg.DisplayName())
	res := updater.TransientSpinResultProgress(ctx, msg,
		func(ctx context.Context, u *fx.Update) error {
			return r.runAwaitingLock(ctx, waitRelabel(u, msg), args...)
		})
	if err := res.Silent(); err != nil {
		return err
	}
	r.cleanup(ctx)
	return updater.CompleteReport(
		res,
		r.cfg.DisplayName(),
		r.cfg.info,
		r.before,
		r.installedVersion(ctx),
	)
}

// reinstall uninstalls any existing copy then installs the chosen channel,
// switching cleanly between stable and dev (--HEAD) builds.
func (r *runner) reinstall(ctx context.Context, head bool) error {
	r.brewSilent(ctx, "uninstall", "--ignore-dependencies", r.cfg.formulaRef())
	return r.install(ctx, head)
}

// install taps (when needed) and installs the formula, optionally from HEAD.
func (r *runner) install(ctx context.Context, head bool) error {
	r.removeTaps(ctx)
	if r.cfg.tap != "" {
		if err := r.tap(ctx); err != nil {
			return err
		}
	}
	args := []string{"install"}
	if head {
		args = append(args, "--HEAD")
	}
	args = append(args, r.cfg.formulaRef())

	msg := fmt.Sprintf("Installing %s", r.cfg.DisplayName())
	if head {
		msg = fmt.Sprintf("Compiling %s from source", r.cfg.DisplayName())
	}
	if err := r.spin(ctx, msg, args...); err != nil {
		return err
	}
	r.cleanup(ctx)
	return r.report(ctx)
}

// removeTaps untaps each stale tap in cfg, best-effort, so a relocated formula
// is not resolved from an old tap. Errors (e.g. a tap not present) are ignored.
func (r *runner) removeTaps(ctx context.Context) {
	for _, t := range r.cfg.removeTaps {
		r.brewSilent(ctx, "untap", t)
	}
}

// tap registers the configured tap (with its git URL for a private tap) so the
// formula resolves. It runs silently (no spinner line) but still returns an
// error, so a genuine tap failure stops the update instead of being masked.
func (r *runner) tap(ctx context.Context) error {
	if r.tapInstalled(ctx) {
		return nil
	}
	args := []string{"tap", r.cfg.tap}
	if r.cfg.tapURL != "" {
		args = append(args, r.cfg.tapURL)
	}
	return r.run(ctx, args...)
}

func (r *runner) tapInstalled(ctx context.Context) bool {
	out, err := r.brewCmd(ctx, "tap").Output()
	if err != nil {
		return false
	}
	return slices.Contains(xstrings.SplitLines(string(out)), r.cfg.tap)
}

// report logs the resulting version, as an old→new pair when it changed. It
// returns nil so callers can `return r.report(ctx)` in the success path.
func (r *runner) report(ctx context.Context) error {
	updater.Report(r.cfg.DisplayName(), r.cfg.info, r.before, r.installedVersion(ctx))
	return nil
}

// installed reports whether brew already manages the formula.
func (r *runner) installed(ctx context.Context) bool {
	return r.brewCmd(ctx, "list", r.cfg.formulaRef()).Run() == nil
}

// installedVersion returns the version reported by the freshly-installed binary
// itself, by executing `<brew-prefix>/bin/<binary> version`. It invokes the
// Homebrew copy by its absolute path - never a bare name - so a stray binary
// earlier on PATH cannot answer in its place.
//
// Reading the version from the binary (rather than Homebrew's keg name) keeps the
// reported "to" version in the same representation as the "from" version
// ([clive.Current]): both are the binary's own git-describe string. That matters
// for a --HEAD build, where Homebrew names the keg "HEAD-<hash>" while the binary
// reports "X.Y.Z-N-g<hash>-dev" - two spellings of one commit that would
// otherwise look like an update when nothing actually changed.
func (r *runner) installedVersion(ctx context.Context) string {
	dir := r.brewBinDir(ctx)
	if dir == "" {
		return ""
	}
	bin := dir + "/" + r.cfg.BinaryName()
	version, err := r.cfg.resolveVersion(ctx, bin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(version)
}

// brewBinDir returns Homebrew's bin directory (<prefix>/bin), or "" when the
// prefix cannot be determined.
func (r *runner) brewBinDir(ctx context.Context) string {
	if prefix := r.brewPrefix(ctx); prefix != "" {
		return prefix + "/bin"
	}
	return ""
}

// brewPrefix returns Homebrew's install prefix, or "" when it cannot be
// determined. The prefix never changes within one update, so the first
// successful lookup is memoized to avoid repeated brew invocations.
func (r *runner) brewPrefix(ctx context.Context) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prefix != "" {
		return r.prefix
	}
	out, err := r.brewCmd(ctx, "--prefix").Output()
	if err != nil {
		return ""
	}
	r.prefix = strings.TrimSpace(string(out))
	return r.prefix
}

// cleanup handles copies of the binary found on PATH outside Homebrew, so the
// brew install is the one that runs. Its action is governed by cfg.onConflict.
// It is best-effort and never fails the update.
func (r *runner) cleanup(ctx context.Context) {
	if r.cfg.onConflict == ConflictIgnore {
		return
	}
	brewBin := r.brewBinDir(ctx)
	if brewBin == "" {
		return
	}

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

// resolveConflict applies cfg.onConflict to a single non-Homebrew copy at path.
// shadows reports whether the copy precedes Homebrew on PATH, and so is the one a
// name lookup actually resolves to. A warn stays silent about a copy that does
// not shadow, since it is harmless; an uninstall trashes every stray copy.
func (r *runner) resolveConflict(path string, shadows bool) {
	if r.cfg.onConflict == ConflictWarn {
		if shadows {
			clog.Warn().
				Path("path", path).
				Msgf("Another copy of %s in your `$PATH` is shadowing the Homebrew install", r.cfg.DisplayName())
		}
		return
	}
	// Trash the stray copy so it can be recovered, falling back to a permanent
	// remove on a platform that cannot trash (e.g. macOS older than 15).
	switch err := xos.Trash(path); {
	case err == nil:
		clog.Info().
			Symbol(updater.TrashSymbol()).
			MessageStyle(updater.MessageStyle()).
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
			Symbol(updater.TrashSymbol()).
			MessageStyle(updater.MessageStyle()).
			Path("path", path).
			Msgf("Removed stray %s installation", r.cfg.DisplayName())
	}
}

// spin runs a mutating brew command under a spinner via [updater.Spin], logging
// a completion line on success and surfacing the error on failure. It waits out
// a concurrent process holding the formula lock (see [runner.runAwaitingLock])
// rather than failing on it.
func (r *runner) spin(ctx context.Context, msg string, args ...string) error {
	return updater.SpinProgress(ctx, msg, func(ctx context.Context, u *fx.Update) error {
		return r.runAwaitingLock(ctx, waitRelabel(u, msg), args...)
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
// clearing the proxy when cfg.noProxy is set.
func (r *runner) brewCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.brew, args...) //nolint:gosec // controlled args
	// We drive the formula refresh ourselves via r.fetch, so suppress brew's
	// implicit pre-command auto-update: it would only repeat that work, and -
	// since it grabs the same `brew update` lock - would reintroduce the very
	// "another process is already running" contention r.fetch waits out.
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_AUTO_UPDATE=1")
	if r.cfg.noProxy {
		cmd.Env = append(cmd.Env, updater.ProxyBypass()...)
	}
	return cmd
}

// BinaryName is the executable/command name, defaulting to the formula. Shared
// by other update mechanisms (the periodic check) that name the `<binary>
// update` command.
func (c Config) BinaryName() string { return cmp.Or(c.binary, c.resolveFormula()) }

func (c Config) resolveVersion(ctx context.Context, bin string) (string, error) {
	if c.versionResolver != nil {
		return c.versionResolver(ctx, bin)
	}
	return defaultResolveVersion(ctx, bin)
}

// defaultResolveVersion asks the binary for its own version: first via the
// near-universal `--version` flag, then via the `version` subcommand for
// tools that only expose the latter.
func defaultResolveVersion(ctx context.Context, bin string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		out, err = exec.CommandContext(ctx, bin, "version").Output()
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DisplayName is the human-facing name used in messages, defaulting to the
// binary (and thus the formula) name when Name is unset.
func (c Config) DisplayName() string { return updater.DisplayName(c.name, c.BinaryName()) }

// VersionLink renders v as a clickable link to its release or commit, delegating
// to the embedded [clive.Info]. It lets [Config] satisfy [updater.Tool].
func (c Config) VersionLink(v string) string { return c.info.VersionLink(v) }

// LatestRef returns the highest semver tag in the tool's repository, delegating
// to [clive.Info.LatestTag]. It lets [Config] satisfy [updater.Tool].
func (c Config) LatestRef(ctx context.Context, client *http.Client) (string, error) {
	return c.info.LatestTag(ctx, client)
}

// formulaRef is the brew install target: tap-qualified when a tap is set.
func (c Config) formulaRef() string {
	if c.tap != "" {
		return c.tap + "/" + c.resolveFormula()
	}
	return c.resolveFormula()
}

// resolveFormula is the Homebrew formula name: [WithFormula] when set, else the
// last element of the module path (e.g. github.com/x/myapp -> myapp), or "" when
// neither is available.
func (c Config) resolveFormula() string {
	if c.formula != "" {
		return c.formula
	}
	if c.info.Module == "" {
		return ""
	}
	return path.Base(c.info.Module)
}
