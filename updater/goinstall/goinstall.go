// Package goinstall self-updates a Go CLI binary through `go install`. A tool
// describes itself with a [Config] and calls [Update], which runs
// `go install <module>@<ref>` to fetch, build, and install the latest release
// into GOBIN, with a stable (@latest) and a dev (@branch) channel. [Check]
// reports whether a newer release exists without installing anything.
//
// It is the `go install` counterpart to updater/brew: a caller picks whichever
// mechanism matches how its tool is distributed and wires the same [clive.Info]
// into either one. Shared UX helpers live in the parent updater package. The
// clog dependency lives here, keeping the core clive package dependency-light
// for version-only consumers.
package goinstall

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
	"path/filepath"
	"strings"
	"time"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
)

// goTimeout bounds a full update; building from source can be slow.
const goTimeout = 5 * time.Minute

// defaultBranch is the dev channel's ref when [WithBranch] is unset.
const defaultBranch = "main"

// Channel selects what ref [Update] installs.
type Channel int

const (
	// Latest installs the latest tagged release (`module@latest`); the default.
	Latest Channel = iota
	// Dev builds and installs the tip of the dev branch (`module@<Branch>`),
	// yielding a pseudo-version build.
	Dev
)

// Config satisfies the metadata interface notify consumes and the
// behavioural [updater.Updater] interface.
var _ updater.Updater = Config{}

// Check implements [updater.Updater].
func (c Config) Check(ctx context.Context) error { return Check(ctx, c) }

// Update implements [updater.Updater]; dev selects the dev branch and stable
// is ignored ([Latest] already is the latest stable release).
func (c Config) Update(ctx context.Context, dev, _ bool) error {
	return Update(ctx, c, ChannelFor(dev))
}

// ChannelFor maps a --dev flag to a Channel; unset is Latest. Unlike Homebrew,
// `go install` has no separate "upgrade" verb - @latest always resolves to the
// newest tag - so stable and the default coincide and only --dev branches off.
func ChannelFor(dev bool) Channel {
	if dev {
		return Dev
	}
	return Latest
}

// Config identifies the tool for a `go install` self-update. Build it with
// [New]: only the module info is required, and optional behaviour is set with
// the With* [Option]s. It satisfies [updater.Tool] for notify.
type Config struct {
	binary  string
	branch  string
	dir     string
	info    clive.Info
	name    string
	noProxy bool
}

// New builds a [Config] for a `go install` self-update. info carries the module
// (the install target, required) and repo used for version checks and release
// links. Optional behaviour is configured with the With* [Option]s.
func New(info clive.Info, opts ...Option) Config {
	c := Config{info: info}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Check reports whether a newer release of cfg is available, without installing.
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

// Update installs the latest cfg via `go install` on the given channel.
func Update(ctx context.Context, cfg Config, channel Channel) error {
	ctx, cancel := context.WithTimeout(ctx, goTimeout)
	defer cancel()

	if cfg.info.Module == "" {
		return fmt.Errorf("updating %s needs Info.Module set", cfg.DisplayName())
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf(
			"updating %s needs the Go toolchain; install it from https://go.dev/dl",
			cfg.DisplayName(),
		)
	}
	r := &runner{cfg: cfg, goBin: goBin, current: clive.Current()}

	verb := "Installing"
	if channel == Dev {
		verb = "Building"
	}
	if err := r.spin(
		ctx,
		fmt.Sprintf("%s latest %s", verb, cfg.DisplayName()),
		"install",
		cfg.installTarget(channel),
	); err != nil {
		return err
	}
	r.report(ctx)
	return nil
}

// runner holds the go invocation state for one update.
type runner struct {
	cfg     Config
	goBin   string
	current string
}

// report logs the resulting version, as an old→new pair when it changed.
func (r *runner) report(ctx context.Context) {
	updater.Report(r.cfg.DisplayName(), r.cfg.info, r.current, r.installedVersion(ctx))
}

// installedVersion reads the version embedded in the freshly-installed binary
// via `go version -m`, or "". This reports the version actually on disk rather
// than re-querying the proxy, so a Dev (@branch) build reports its real
// pseudo-version and a race against a new release cannot misreport.
func (r *runner) installedVersion(ctx context.Context) string {
	bin := r.installedPath(ctx)
	if bin == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, r.goBin, "version", "-m", bin) //nolint:gosec // controlled args
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return moduleVersion(out)
}

// installedPath returns the path `go install` wrote the binary to. We export
// GOBIN as cfg's install dir, so that is where it lands; only when the dir
// cannot be resolved (no home directory, unset Dir) do we fall back to reading
// go's own default of the first $GOPATH entry's bin directory.
func (r *runner) installedPath(ctx context.Context) string {
	if dir := r.cfg.installDir(); dir != "" {
		return filepath.Join(dir, r.cfg.BinaryName())
	}
	gopath := r.goEnv(ctx, "GOPATH")
	if gopath == "" {
		return ""
	}
	// GOPATH may list several roots; go install writes to the first.
	first, _, _ := strings.Cut(gopath, string(os.PathListSeparator))
	return filepath.Join(first, "bin", r.cfg.BinaryName())
}

// goEnv returns a trimmed `go env <key>` value, or "" on error.
func (r *runner) goEnv(ctx context.Context, key string) string {
	cmd := exec.CommandContext(ctx, r.goBin, "env", key) //nolint:gosec // controlled args
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// moduleVersion extracts the main module's version from `go version -m` output,
// whose "mod" line reads "\tmod\t<module>\t<version>\t<hash>". Returns "" when
// absent (e.g. a binary built without module info).
func moduleVersion(data []byte) string {
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "mod" {
			return fields[2]
		}
	}
	return ""
}

// spin runs a go command under a spinner via [updater.Spin], logging a
// completion line on success and surfacing [runner.run]'s error on failure.
func (r *runner) spin(ctx context.Context, msg string, args ...string) error {
	return updater.Spin(ctx, msg, func(ctx context.Context) error {
		return r.run(ctx, args...)
	})
}

// run executes a go command without any logging, capturing stderr so a failure
// carries go's own message rather than a bare "exit status 1". In verbose mode
// the command's output is also streamed to the terminal.
func (r *runner) run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := r.goCmd(ctx, args...)
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

// goCmd builds a go command, scoping GOPRIVATE to the module for a private repo
// and clearing the proxy when cfg.noProxy is set.
func (r *runner) goCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.goBin, args...) //nolint:gosec // controlled args
	cmd.Env = os.Environ()
	if dir := r.cfg.installDir(); dir != "" {
		cmd.Env = append(cmd.Env, "GOBIN="+dir)
	}
	if r.cfg.info.Private {
		cmd.Env = append(
			cmd.Env,
			"GOPRIVATE="+updater.GoPrivate(r.cfg.info.Module, os.Getenv("GOPRIVATE")),
		)
	}
	if r.cfg.noProxy {
		cmd.Env = append(cmd.Env, updater.ProxyBypass()...)
	}
	return cmd
}

// BinaryName is the executable/command name, defaulting to the last element of
// the module path. Shared by other update mechanisms (the periodic check) that
// name the `<binary> update` command.
func (c Config) BinaryName() string {
	if c.binary != "" {
		return c.binary
	}
	return path.Base(c.info.Module)
}

// DisplayName is the human-facing name used in messages, defaulting to the
// binary (and thus the module) name when Name is unset.
func (c Config) DisplayName() string { return updater.DisplayName(c.name, c.BinaryName()) }

// VersionLink renders v as a clickable link to its release or commit, delegating
// to the embedded [clive.Info]. It lets [Config] satisfy [updater.Tool].
func (c Config) VersionLink(v string) string { return c.info.VersionLink(v) }

// LatestRef returns the highest semver tag in the tool's repository, delegating
// to [clive.Info.LatestTag]. It lets [Config] satisfy [updater.Tool].
func (c Config) LatestRef(ctx context.Context, client *http.Client) (string, error) {
	return c.info.LatestTag(ctx, client)
}

// resolveBranch is the Dev channel's ref, defaulting to defaultBranch.
func (c Config) resolveBranch() string { return cmp.Or(c.branch, defaultBranch) }

// installDir is the GOBIN the binary is installed into via [updater.InstallDir]:
// Dir when set (with a leading "~" and any env vars expanded), else ~/.local/bin.
// It returns "" only when no Dir was given and the home directory cannot be
// resolved, leaving GOBIN unset so go install falls back to its own default.
func (c Config) installDir() string { return updater.InstallDir(c.dir) }

// installTarget is the `go install` argument for channel: "module@latest" for
// the stable channel, "module@<branch>" for dev.
func (c Config) installTarget(channel Channel) string {
	ref := "latest"
	if channel == Dev {
		ref = c.resolveBranch()
	}
	return c.info.Module + "@" + ref
}
