package brew

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gechr/clive"
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
		"printf '%s\\n' \"$*\" >> " + shellQuote(log) + "\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo " + shellQuote(prefix) + " ;;\n" +
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
	require.Contains(t, strings.Split(strings.TrimSpace(string(out)), "\n"), "upgrade example/tap/app")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
