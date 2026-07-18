package brew

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
	xshell "github.com/gechr/x/shell"
	xstrings "github.com/gechr/x/strings"
	"github.com/stretchr/testify/require"
)

func TestUpgradeUsesFormulaRef(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "brew.log")
	prefix := filepath.Join(dir, "prefix")
	require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o755))

	binary := filepath.Join(prefix, "bin", "app")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho 1.0.0\n"), 0o755))

	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + xshell.Quote(log) + "\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"  list) exit 0 ;;\n" +
		"  upgrade) exit 0 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{
		before: "1.0.0",
		brew:   brew,
		cfg: New(
			clive.Info{},
			WithFormula("app"),
			WithTap("example/tap"),
			WithOnConflict(ConflictIgnore),
		),
		current: "1.0.0",
		// Update probes presence concurrently with the fetch; upgrade
		// consumes the result.
		present: true,
	}
	require.NoError(t, r.upgrade(context.Background()))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{
		// --prefix leads: the formula-lock pre-check resolves the locks directory
		// before running the upgrade.
		"--prefix",
		"upgrade example/tap/app",
	}, commandLog(out))
}

func TestInstallSkipsExistingTap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "brew.log")
	prefix := filepath.Join(dir, "prefix")
	require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o755))

	binary := filepath.Join(prefix, "bin", "app")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho 1.0.0\n"), 0o755))

	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + xshell.Quote(log) + "\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"  tap) [ \"$#\" -eq 1 ] && echo example/tap ;;\n" +
		"  install) exit 0 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{
		before: "1.0.0",
		brew:   brew,
		cfg: New(
			clive.Info{},
			WithFormula("app"),
			WithTap("example/tap"),
			WithTapURL("git@example.com:example/tap"),
			WithOnConflict(ConflictIgnore),
		),
		current: "1.0.0",
	}
	require.NoError(t, r.install(context.Background(), false))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{
		"tap",
		// --prefix leads the install: the formula-lock pre-check resolves the locks
		// directory before running it.
		"--prefix",
		"install example/tap/app",
	}, commandLog(out))
}

func TestInstalledVersionUsesDefaultResolver(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "brew.log")
	prefix := filepath.Join(dir, "prefix")
	require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o755))

	binary := filepath.Join(prefix, "bin", "app")
	require.NoError(
		t,
		os.WriteFile(
			binary,
			[]byte("#!/bin/sh\necho \"$*\" >> "+xshell.Quote(log)+"\necho v0.2.3\n"),
			0o755,
		),
	)

	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{brew: brew, cfg: New(clive.Info{}, WithFormula("app"))}
	require.Equal(t, "v0.2.3", r.installedVersion(context.Background()))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{"--version"}, commandLog(out))
}

func TestInstalledVersionFallsBackToVersionSubcommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	prefix := filepath.Join(dir, "prefix")
	require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o755))

	// A CLI without a --version flag: only the `version` subcommand answers.
	binary := filepath.Join(prefix, "bin", "app")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> " + xshell.Quote(log) + "\n" +
		"[ \"$1\" = version ] || exit 1\n" +
		"echo v0.4.9\n"
	require.NoError(t, os.WriteFile(binary, []byte(script), 0o755))

	brew := filepath.Join(dir, "brew")
	brewScript := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(brewScript), 0o755))

	r := &runner{brew: brew, cfg: New(clive.Info{}, WithFormula("app"))}
	require.Equal(t, "v0.4.9", r.installedVersion(context.Background()))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{"--version", "version"}, commandLog(out))
}

func TestInstalledVersionUsesConfiguredResolver(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	prefix := filepath.Join(dir, "prefix")
	require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o755))

	binary := filepath.Join(prefix, "bin", "app")
	require.NoError(t, os.WriteFile(
		binary,
		[]byte(
			"#!/bin/sh\necho \"$*\" >> "+xshell.Quote(
				log,
			)+"\nprintf '{\"version\":\"1.2.3\"}\\n'\n",
		),
		0o755,
	))

	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{
		brew: brew,
		cfg: New(
			clive.Info{},
			WithFormula("app"),
			WithResolveVersionFunc(func(ctx context.Context, bin string) (string, error) {
				out, err := exec.CommandContext(ctx, bin, "version", "--json").Output()
				if err != nil {
					return "", err
				}
				var payload struct {
					Version string `json:"version"`
				}
				if err := json.Unmarshal(out, &payload); err != nil {
					return "", err
				}
				return payload.Version, nil
			}),
		),
	}
	require.Equal(t, "1.2.3", r.installedVersion(context.Background()))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{"version --json"}, commandLog(out))
}

func TestUpdateWaitsOutBrewLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "brew.log")
	prefix := filepath.Join(dir, "prefix")
	// The lock file exists but is not flocked, so waitUpdateLock sees it free and
	// returns at once - exercising the wait path without a concurrent holder.
	locks := filepath.Join(prefix, "var", "homebrew", "locks")
	require.NoError(t, os.MkdirAll(locks, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(locks, "update"), nil, 0o644))

	// `brew update` fails as brew does when another process holds the lock; every
	// other subcommand succeeds.
	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + xshell.Quote(log) + "\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"  update) echo 'Another brew update process is already running.' >&2; exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{brew: brew, cfg: New(clive.Info{}, WithFormula("app"))}

	// The held lock is tolerated: update returns nil (skip, not fail) and never
	// re-runs `brew update` - the concurrent process already refreshed the metadata.
	require.NoError(t, r.update(context.Background(), nil))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{"update --quiet", "--prefix"}, commandLog(out))
}

func TestRunAwaitingLockRetriesPastFormulaLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "brew.log")
	counter := filepath.Join(dir, "n")
	prefix := filepath.Join(dir, "prefix")

	// `brew upgrade` fails the first time as brew does when another process holds
	// the formula lock, then succeeds - modelling that other process finishing.
	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + xshell.Quote(log) + "\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + xshell.Quote(prefix) + " ;;\n" +
		"  upgrade)\n" +
		"    n=$(cat " + xshell.Quote(counter) + " 2>/dev/null || echo 0); n=$((n+1));" +
		" echo \"$n\" > " + xshell.Quote(counter) + "\n" +
		"    [ \"$n\" -eq 1 ] && { echo 'A `brew upgrade` process has already locked /x.' >&2; exit 1; }\n" +
		"    exit 0 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{brew: brew, cfg: New(clive.Info{}, WithFormula("app"))}

	// The lock-held failure is waited out and retried, not surfaced: the command
	// runs twice and ultimately succeeds. The leading --prefix is the formula-lock
	// pre-check resolving Homebrew's locks directory.
	require.NoError(t, r.runAwaitingLock(context.Background(), nil, "upgrade", "app"))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{"--prefix", "upgrade app", "upgrade app"}, commandLog(out))
}

func TestUpgradeRewritesContextDeadlineExceeded(t *testing.T) {
	buf := captureDefault(t)
	err := upgradeTimeoutError(context.DeadlineExceeded)

	require.ErrorIs(t, err, updater.ErrReported)
	require.EqualError(t, err, "update failed: Timed out while waiting for upgrade")
	require.Equal(t, "ERR ❌ Timed out while waiting for upgrade elapsed=5m\n", buf.String())
}

func captureDefault(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	prev := clog.Default
	clog.Default = clog.New(clog.TestOutput(&buf))
	t.Cleanup(func() { clog.Default = prev })
	return &buf
}

func TestUpdateSurfacesNonLockError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	brew := filepath.Join(dir, "brew")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = update ] && { echo 'fatal: network unreachable' >&2; exit 1; }\n"
	require.NoError(t, os.WriteFile(brew, []byte(script), 0o755))

	r := &runner{brew: brew, cfg: New(clive.Info{}, WithFormula("app"))}

	// A genuine `brew update` failure is not mistaken for a lock and is returned
	// verbatim rather than swallowed.
	err := r.update(context.Background(), nil)
	require.EqualError(t, err, "fatal: network unreachable")
}

func TestIsBrewLocked(t *testing.T) {
	t.Parallel()

	require.False(t, isBrewLocked(nil))
	require.False(t, isBrewLocked(errors.New("exit status 1")))
	// Homebrew's own messages, verbatim - capitalised and punctuated, hence built
	// as values rather than errors.New literals (which revive would flag).
	running := "Another brew update process is already running."
	locked := "lockf: 200: already locked"
	require.True(t, isBrewLocked(errors.New(running)))
	require.True(t, isBrewLocked(errors.New(locked)))
}

func commandLog(out []byte) []string {
	return xstrings.SplitLines(string(out))
}
