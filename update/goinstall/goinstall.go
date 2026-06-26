// Package goinstall self-updates a Go CLI binary through `go install`. A tool
// describes itself with a [Config] and calls [Update], which runs
// `go install <module>@<ref>` to fetch, build, and install the latest release
// into GOBIN, with a stable (@latest) and a dev (@branch) channel. [Check]
// reports whether a newer release exists without installing anything.
//
// It is the `go install` counterpart to update/brew: a caller picks whichever
// mechanism matches how its tool is distributed and wires the same [clive.Info]
// into either one. The clog dependency lives here, keeping the core clive
// package dependency-light for version-only consumers.
package goinstall

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gechr/clive"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
	xfilepath "github.com/gechr/x/filepath"
)

// goTimeout bounds a full update; building from source can be slow.
const goTimeout = 5 * time.Minute

// defaultBranch is the dev channel's ref when [Config.Branch] is unset.
const defaultBranch = "main"

// defaultDir is the install directory when [Config.Dir] is unset, relative to
// the user's home. `go install` defaults to $GOPATH/bin, which is often not on
// PATH; ~/.local/bin is the conventional per-user binary directory.
const defaultDir = ".local/bin"

// Channel selects what ref [Update] installs.
type Channel int

const (
	// Latest installs the latest tagged release (`module@latest`); the default.
	Latest Channel = iota
	// Dev builds and installs the tip of the dev branch (`module@<Branch>`),
	// yielding a pseudo-version build.
	Dev
)

// ChannelFor maps a --dev flag to a Channel; unset is Latest. Unlike Homebrew,
// `go install` has no separate "upgrade" verb - @latest always resolves to the
// newest tag - so stable and the default coincide and only --dev branches off.
func ChannelFor(dev bool) Channel {
	if dev {
		return Dev
	}
	return Latest
}

// Config identifies the tool for a `go install` self-update. Only Info.Module is
// required; everything else has a sensible default derived from the module path.
type Config struct {
	// Info carries the module path and repo for version checks and release links.
	// Info.Module is the `go install` target and is required. Info.Private routes
	// the install through direct version control (GOPRIVATE) rather than the
	// public module proxy.
	Info clive.Info
	// Name is the display name shown in messages. Defaults to the binary name.
	Name string
	// Binary is the executable name written to GOBIN, used to locate the result
	// for version reporting. Defaults to the last element of the module path.
	Binary string
	// Dir is the directory the binary is installed into, exported to the install
	// as GOBIN so it lands somewhere on PATH rather than $GOPATH/bin. A leading
	// "~" and any $ENV references are expanded. Defaults to ~/.local/bin.
	Dir string
	// Branch is the ref the Dev channel installs from; defaults to "main".
	Branch string
	// NoProxy clears the proxy variables for the go subprocess, so an update
	// bypasses an HTTP proxy that cannot reach the module's source.
	NoProxy bool
}

// Check reports whether a newer release of cfg is available, without installing.
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

// Update installs the latest cfg via `go install` on the given channel.
func Update(ctx context.Context, cfg Config, channel Channel) error {
	ctx, cancel := context.WithTimeout(ctx, goTimeout)
	defer cancel()

	if cfg.Info.Module == "" {
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
	old := version.RemovePrefix(r.current)
	current := version.RemovePrefix(r.installedVersion(ctx))
	if old != "" && current != "" && old != current {
		clog.Info().
			Symbol("🎉").
			Str("old", r.cfg.Info.VersionLink(old)).
			Str("new", r.cfg.Info.VersionLink(current)).
			Msgf("Updated %s", r.cfg.DisplayName())
		return
	}
	upToDate(r.cfg.DisplayName(), r.cfg.Info, cmp.Or(current, old))
}

// upToDate warns that no update was applied, including the version field only
// when a version is known.
func upToDate(name string, info clive.Info, ver string) {
	e := clog.Warn()
	if ver != "" {
		e = e.Str("version", info.VersionLink(ver))
	}
	e.Msgf("%s is already up-to-date", name)
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

// spin runs a go command under a spinner: it logs a completion line on success,
// and on failure returns the error (via [runner.run]) for the caller to surface.
// The failure path uses Silent so the spinner does not log its own error line,
// leaving the caller to report the failure exactly once.
func (r *runner) spin(ctx context.Context, msg string, args ...string) error {
	res := clog.Spinner(msg).Elapsed("elapsed").Wait(ctx, func(ctx context.Context) error {
		return r.run(ctx, args...)
	})
	if err := res.Silent(); err != nil {
		return err
	}
	return res.Msg(msg)
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
// and clearing the proxy when cfg.NoProxy is set.
func (r *runner) goCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.goBin, args...) //nolint:gosec // controlled args
	cmd.Env = os.Environ()
	if dir := r.cfg.installDir(); dir != "" {
		cmd.Env = append(cmd.Env, "GOBIN="+dir)
	}
	if r.cfg.Info.Private {
		cmd.Env = append(cmd.Env, "GOPRIVATE="+goPrivate(r.cfg.Info.Module, os.Getenv("GOPRIVATE")))
	}
	if r.cfg.NoProxy {
		cmd.Env = append(cmd.Env, proxyBypass()...)
	}
	return cmd
}

// proxyBypass returns env entries that disable any inherited HTTP proxy: each
// proxy variable is blanked (an empty value overrides the inherited one) and
// NO_PROXY is set to "*" so every host is exempt. Both cases are set, as tools
// read either.
func proxyBypass() []string {
	return []string{
		"HTTP_PROXY=", "http_proxy=",
		"HTTPS_PROXY=", "https_proxy=",
		"ALL_PROXY=", "all_proxy=",
		"NO_PROXY=*", "no_proxy=*",
	}
}

// goPrivate returns a GOPRIVATE value that includes module, preserving any
// existing entries so the caller's configuration is not discarded.
func goPrivate(module, existing string) string {
	module = strings.TrimSpace(module)
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return module
	}
	return module + "," + existing
}

// BinaryName is the executable/command name, defaulting to the last element of
// the module path. Shared by other update mechanisms (the periodic check) that
// name the `<binary> update` command.
func (c Config) BinaryName() string {
	if c.Binary != "" {
		return c.Binary
	}
	return path.Base(c.Info.Module)
}

// DisplayName is the human-facing name used in messages, defaulting to the
// binary (and thus the module) name when Name is unset.
func (c Config) DisplayName() string { return cmp.Or(c.Name, c.BinaryName()) }

// branch is the Dev channel's ref, defaulting to defaultBranch.
func (c Config) branch() string { return cmp.Or(c.Branch, defaultBranch) }

// installDir is the GOBIN the binary is installed into: Dir when set (with a
// leading "~/" and any env vars expanded), else ~/.local/bin. It returns "" only
// when the home directory cannot be resolved and no Dir was given, leaving GOBIN
// unset so go install falls back to its own default.
func (c Config) installDir() string {
	if c.Dir != "" {
		return xfilepath.Expand(c.Dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, defaultDir)
}

// installTarget is the `go install` argument for channel: "module@latest" for
// the stable channel, "module@<branch>" for dev.
func (c Config) installTarget(channel Channel) string {
	ref := "latest"
	if channel == Dev {
		ref = c.branch()
	}
	return c.Info.Module + "@" + ref
}
