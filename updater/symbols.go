package updater

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clog"
)

// symbolSet is the private set of glyphs the updater stamps on its own log
// lines, overriding clog's per-level default glyph for those specific entries.
// Each glyph may also carry an explicit colour: since Report emits every
// outcome at the same clog level, a per-outcome colour cannot come from clog's
// level styling, so the colour is baked into the glyph string itself (like the
// version fields in updateLinks). A nil colour leaves clog's per-level symbol
// style to govern, which is the default. The set is aligned with a host's own
// glyph set through the With* options and [SetSymbols]; it is never exposed
// directly.
type symbolSet struct {
	upToDate   string // "already up-to-date" report (default 🚀)
	upgraded   string // version increase (default ⬆️)
	downgraded string // version decrease (default ⬇️)
	done       string // completed spinner step (default ✅)
	trash      string // stray non-Homebrew copy trashed/removed (default 🗑️)

	upToDateColor   color.Color // nil ⇒ clog's per-level symbol style
	upgradedColor   color.Color
	downgradedColor color.Color
	doneColor       color.Color
	trashColor      color.Color

	messageColor color.Color // nil ⇒ normal (plain) message text
}

// symbols is the package-level active set read by Report, UpToDate, Spin, and
// their completion-line variants. It starts at the defaults and is mutated only
// by the With* options applied through SetSymbols.
var symbols = symbolSet{
	upToDate:   "🚀",
	upgraded:   "⬆️",
	downgraded: "⬇️",
	done:       "✅",
	trash:      "🗑️",

	trashColor: lipgloss.Color("3"), // yellow by default
}

// colored returns sym rendered in c when a colour is set and colours are
// enabled; otherwise the raw glyph, so an unset colour leaves clog's per-level
// symbol style in charge. The colour is baked in because it must survive clog
// wrapping the symbol in its level style - the innermost sequence paints the
// glyph, so this override wins.
func colored(sym string, c color.Color) string {
	if c == nil || clog.ColorsDisabled() {
		return sym
	}
	return lipgloss.NewStyle().Foreground(c).Render(sym)
}

func (s symbolSet) styledUpToDate() string   { return colored(s.upToDate, s.upToDateColor) }
func (s symbolSet) styledUpgraded() string   { return colored(s.upgraded, s.upgradedColor) }
func (s symbolSet) styledDowngraded() string { return colored(s.downgraded, s.downgradedColor) }
func (s symbolSet) styledDone() string       { return colored(s.done, s.doneColor) }
func (s symbolSet) styledTrash() string      { return colored(s.trash, s.trashColor) }

// messageStyle returns the style for an update line's message text: plain by
// default, or coloured when WithMessageColor is set. It replaces (not nests
// inside) clog's per-level message style, so the host's level colour never
// leaks onto the updater's lines.
func (s symbolSet) messageStyle() *lipgloss.Style {
	st := lipgloss.NewStyle()
	if s.messageColor != nil {
		st = st.Foreground(s.messageColor)
	}
	return &st
}

// SymbolOption overrides one of the updater's log glyphs (or its colour). Apply
// it with [SetSymbols].
type SymbolOption func()

// WithUpToDateSymbol overrides the "already up-to-date" glyph (default 🚀).
func WithUpToDateSymbol(s string) SymbolOption {
	return func() { symbols.upToDate = s }
}

// WithUpgradedSymbol overrides the upgrade glyph (default ⬆️).
func WithUpgradedSymbol(s string) SymbolOption {
	return func() { symbols.upgraded = s }
}

// WithDowngradedSymbol overrides the downgrade glyph (default ⬇️).
func WithDowngradedSymbol(s string) SymbolOption {
	return func() { symbols.downgraded = s }
}

// WithDoneSymbol overrides the completed-spinner-step glyph (default ✅).
func WithDoneSymbol(s string) SymbolOption {
	return func() { symbols.done = s }
}

// WithTrashSymbol overrides the glyph on the line reporting a stray non-Homebrew
// copy being trashed or removed (default 🗑️).
func WithTrashSymbol(s string) SymbolOption {
	return func() { symbols.trash = s }
}

// WithUpToDateColor colours the "already up-to-date" glyph; nil (the default)
// leaves clog's per-level symbol style in charge.
func WithUpToDateColor(c color.Color) SymbolOption {
	return func() { symbols.upToDateColor = c }
}

// WithUpgradedColor colours the upgrade glyph; nil (the default) leaves clog's
// per-level symbol style in charge.
func WithUpgradedColor(c color.Color) SymbolOption {
	return func() { symbols.upgradedColor = c }
}

// WithDowngradedColor colours the downgrade glyph; nil (the default) leaves
// clog's per-level symbol style in charge.
func WithDowngradedColor(c color.Color) SymbolOption {
	return func() { symbols.downgradedColor = c }
}

// WithDoneColor colours the completed-spinner-step glyph; nil (the default)
// leaves clog's per-level symbol style in charge.
func WithDoneColor(c color.Color) SymbolOption {
	return func() { symbols.doneColor = c }
}

// WithTrashColor colours the stray-copy glyph; nil (the default) leaves clog's
// per-level symbol style in charge.
func WithTrashColor(c color.Color) SymbolOption {
	return func() { symbols.trashColor = c }
}

// WithMessageColor colours the message text on the updater's lines. The default
// (nil) renders the message plain, ignoring the host's per-level message style
// so the updater's lines read consistently regardless of the host's theme.
func WithMessageColor(c color.Color) SymbolOption {
	return func() { symbols.messageColor = c }
}

// TrashSymbol returns the current trash glyph, already coloured if a colour was
// set, for the sub-packages (e.g. brew) that emit the stray-copy line
// themselves rather than through this package's shared helpers.
func TrashSymbol() string { return symbols.styledTrash() }

// MessageStyle returns the message-text style for the updater's lines, for the
// sub-packages (e.g. brew) that emit a line themselves and want it to match the
// shared helpers' message styling.
func MessageStyle() *lipgloss.Style { return symbols.messageStyle() }

// SetSymbols applies opts to the package-level glyph set used by Report,
// UpToDate, Spin, and their completion-line variants. Only the glyphs (and
// colours) named by opts change; everything unspecified keeps its current
// value, so a caller overrides just what it needs and the rest retain their
// defaults.
func SetSymbols(opts ...SymbolOption) {
	for _, o := range opts {
		o()
	}
}
