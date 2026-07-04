package brew

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gechr/clive"
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
	}
	require.NoError(t, r.upgrade(context.Background()))

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, []string{
		"list example/tap/app",
		"upgrade example/tap/app",
		"--prefix",
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
		"install example/tap/app",
		"--prefix",
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
	require.Equal(t, []string{"version"}, commandLog(out))
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

func commandLog(out []byte) []string {
	return xstrings.SplitLines(string(out))
}
