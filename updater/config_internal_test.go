package updater

import (
	"bytes"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clog"
	"github.com/stretchr/testify/require"
)

// useSymbolSet points clog's default logger at a fresh logger declaring set,
// restoring the previous default afterwards.
func useSymbolSet(t *testing.T, set clog.SymbolSet) {
	t.Helper()
	l := clog.New(clog.TestOutput(&bytes.Buffer{}))
	l.SetSymbolSet(set)
	prev := clog.Default()
	clog.SetDefault(l)
	t.Cleanup(func() { clog.SetDefault(prev) })
}

func TestActiveGlyphsFollowClogSymbolSet(t *testing.T) {
	useSymbolSet(t, clog.SymbolSetEmoji)
	require.Equal(t, emojiGlyphs, activeGlyphs())

	clog.SetSymbolSet(clog.SymbolSetText)
	require.Equal(t, textGlyphs, activeGlyphs())
}

func TestDefaultConfigInheritsTextGlyphs(t *testing.T) {
	useSymbolSet(t, clog.SymbolSetText)

	c := Config{}
	require.Equal(t, colored("✔︎", green), c.styledUpToDate())
	require.Equal(t, colored("↑", green), c.styledUpgraded())
	require.Equal(t, colored("↓", red), c.styledDowngraded())
	require.Equal(t, colored("✔︎", green), c.styledDone())
	require.Equal(t, colored("✘", yellow), c.styledTrash())
}

func TestDefaultConfigInheritsEmojiGlyphs(t *testing.T) {
	useSymbolSet(t, clog.SymbolSetEmoji)

	c := Config{}
	require.Equal(t, "🚀", c.styledUpToDate())
	require.Equal(t, "⬆️", c.styledUpgraded())
	require.Equal(t, "⬇️", c.styledDowngraded())
	require.Equal(t, "✅", c.styledDone())
	require.Equal(t, colored("🗑️", yellow), c.styledTrash())
}

func TestGlyphOverrideBeatsSymbolSet(t *testing.T) {
	useSymbolSet(t, clog.SymbolSetText)

	c := Config{}
	WithUpgradedSymbol("⇡")(&c)
	WithDoneColor(nil)(&c)
	WithDowngradedColor(lipgloss.Color("5"))(&c)

	require.Equal(t, colored("⇡", green), c.styledUpgraded(),
		"an overridden symbol should keep the set's colour")
	require.Equal(t, "✔︎", c.styledDone(),
		"an explicit nil colour should leave clog's level style in charge")
	require.Equal(t, colored("↓", lipgloss.Color("5")), c.styledDowngraded(),
		"an overridden colour should keep the set's symbol")
}
