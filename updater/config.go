package updater

import (
	"cmp"
	"image/color"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clog"
)

// Config is the private set of updater-wide knobs: the glyphs it stamps on
// its own log lines (overriding clog's per-level default glyph for those
// specific entries) and the spinner timing thresholds. Each glyph may also
// carry an explicit colour: since Report emits every outcome at the same
// clog level, a per-outcome colour cannot come from clog's level styling, so
// the colour is baked into the glyph string itself (like the version fields
// in updateLinks). Unless a host overrides them through the With* options and
// [SetConfig], the glyphs follow the symbol set declared on clog's default
// logger (see [clog.SymbolSet]): emoji by default, or text glyphs once the
// host applies a text preset such as [clog.TersePreset]. The set is never
// exposed directly.
type Config struct {
	upToDate   glyphOverride // "already up-to-date" report
	upgraded   glyphOverride // version increase
	downgraded glyphOverride // version decrease
	done       glyphOverride // completed spinner step
	trash      glyphOverride // stray non-Homebrew copy trashed/removed

	messageColor color.Color // nil ⇒ normal (plain) message text

	// elapsedMinimum hides a spinner's elapsed field unless the step took at
	// least this long, so quick steps stay uncluttered.
	elapsedMinimum time.Duration
	// gradientMax is the duration mapped to the end of the elapsed and
	// duration gradients on spinner lines.
	gradientMax time.Duration
}

// glyph is a symbol and its colour. A nil colour leaves clog's per-level
// symbol style to govern.
type glyph struct {
	symbol string
	color  color.Color
}

// glyphOverride is a host's override of one glyph. Each part left unset falls
// back to the glyph set matching clog's declared symbol set.
type glyphOverride struct {
	symbol   string      // "" ⇒ inherit the symbol
	color    color.Color // used only when colorSet
	colorSet bool        // true ⇒ color applies, even when nil
}

// resolve renders the glyph, filling unset parts from def.
func (o glyphOverride) resolve(def glyph) string {
	clr := def.color
	if o.colorSet {
		clr = o.color
	}
	return colored(cmp.Or(o.symbol, def.symbol), clr)
}

// glyphSet is one family of the updater's glyphs.
type glyphSet struct {
	upToDate, upgraded, downgraded, done, trash glyph
}

var (
	green  = lipgloss.Color("2")
	red    = lipgloss.Color("1")
	yellow = lipgloss.Color("3")
)

// emojiGlyphs match clog's default emoji symbols ([clog.SymbolSetEmoji]).
var emojiGlyphs = glyphSet{
	upToDate:   glyph{symbol: "🚀"},
	upgraded:   glyph{symbol: "⬆️"},
	downgraded: glyph{symbol: "⬇️"},
	done:       glyph{symbol: "✅"},
	trash:      glyph{symbol: "🗑️", color: yellow},
}

// textGlyphs match clog's single-width text symbols ([clog.SymbolSetText]).
var textGlyphs = glyphSet{
	upToDate:   glyph{symbol: "✔︎", color: green},
	upgraded:   glyph{symbol: "↑", color: green},
	downgraded: glyph{symbol: "↓", color: red},
	done:       glyph{symbol: "✔︎", color: green},
	trash:      glyph{symbol: "✘", color: yellow},
}

// activeGlyphs returns the glyph set matching the symbol set declared on
// clog's default logger. It is read on every line, so the order in which a
// host applies its clog preset and [SetConfig] does not matter.
func activeGlyphs() glyphSet {
	if clog.GetSymbolSet() == clog.SymbolSetText {
		return textGlyphs
	}
	return emojiGlyphs
}

const (
	// defaultElapsedMinimum is the default for [Config.elapsedMinimum].
	defaultElapsedMinimum = 3 * time.Second
	// defaultGradientMax is the default for [Config.gradientMax].
	defaultGradientMax = 30 * time.Second
)

// cfg is the package-level active configuration read by Report, UpToDate,
// Spin, and their completion-line variants. It starts at the defaults and is
// mutated only by the With* options applied through SetConfig.
var cfg = Config{
	elapsedMinimum: defaultElapsedMinimum,
	gradientMax:    defaultGradientMax,
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

func (c Config) styledUpToDate() string   { return c.upToDate.resolve(activeGlyphs().upToDate) }
func (c Config) styledUpgraded() string   { return c.upgraded.resolve(activeGlyphs().upgraded) }
func (c Config) styledDowngraded() string { return c.downgraded.resolve(activeGlyphs().downgraded) }
func (c Config) styledDone() string       { return c.done.resolve(activeGlyphs().done) }
func (c Config) styledTrash() string      { return c.trash.resolve(activeGlyphs().trash) }

// messageStyle returns the style for an update line's message text: plain by
// default, or coloured when WithMessageColor is set. It replaces (not nests
// inside) clog's per-level message style, so the host's level colour never
// leaks onto the updater's lines.
func (c Config) messageStyle() *lipgloss.Style {
	st := lipgloss.NewStyle()
	if c.messageColor != nil {
		st = st.Foreground(c.messageColor)
	}
	return &st
}

// ConfigOption overrides one of the updater's log glyphs, colours, or spinner
// timing thresholds. Apply it with [SetConfig].
type ConfigOption func(*Config)

// WithUpToDateSymbol overrides the "already up-to-date" glyph (default 🚀,
// or ✔︎ under [clog.SymbolSetText]).
func WithUpToDateSymbol(s string) ConfigOption {
	return func(c *Config) { c.upToDate.symbol = s }
}

// WithUpgradedSymbol overrides the upgrade glyph (default ⬆️, or ↑ under
// [clog.SymbolSetText]).
func WithUpgradedSymbol(s string) ConfigOption {
	return func(c *Config) { c.upgraded.symbol = s }
}

// WithDowngradedSymbol overrides the downgrade glyph (default ⬇️, or ↓ under
// [clog.SymbolSetText]).
func WithDowngradedSymbol(s string) ConfigOption {
	return func(c *Config) { c.downgraded.symbol = s }
}

// WithDoneSymbol overrides the completed-spinner-step glyph (default ✅, or ✔︎
// under [clog.SymbolSetText]).
func WithDoneSymbol(s string) ConfigOption {
	return func(c *Config) { c.done.symbol = s }
}

// WithTrashSymbol overrides the glyph on the line reporting a stray non-Homebrew
// copy being trashed or removed (default 🗑️, or ✘ under [clog.SymbolSetText]).
func WithTrashSymbol(s string) ConfigOption {
	return func(c *Config) { c.trash.symbol = s }
}

// WithUpToDateColor colours the "already up-to-date" glyph (default none, or
// green under [clog.SymbolSetText]); nil leaves clog's per-level symbol style
// in charge.
func WithUpToDateColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.upToDate.color, c.upToDate.colorSet = clr, true }
}

// WithUpgradedColor colours the upgrade glyph (default none, or green under
// [clog.SymbolSetText]); nil leaves clog's per-level symbol style in charge.
func WithUpgradedColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.upgraded.color, c.upgraded.colorSet = clr, true }
}

// WithDowngradedColor colours the downgrade glyph (default none, or red under
// [clog.SymbolSetText]); nil leaves clog's per-level symbol style in charge.
func WithDowngradedColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.downgraded.color, c.downgraded.colorSet = clr, true }
}

// WithDoneColor colours the completed-spinner-step glyph (default none, or
// green under [clog.SymbolSetText]); nil leaves clog's per-level symbol style
// in charge.
func WithDoneColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.done.color, c.done.colorSet = clr, true }
}

// WithTrashColor colours the stray-copy glyph (default yellow); nil leaves
// clog's per-level symbol style in charge.
func WithTrashColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.trash.color, c.trash.colorSet = clr, true }
}

// WithMessageColor colours the message text on the updater's lines. The default
// (nil) renders the message plain, ignoring the host's per-level message style
// so the updater's lines read consistently regardless of the host's theme.
func WithMessageColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.messageColor = clr }
}

// WithElapsedMinimum overrides the minimum duration a spinner step must run
// before its elapsed field is shown (default 3s).
func WithElapsedMinimum(d time.Duration) ConfigOption {
	return func(c *Config) { c.elapsedMinimum = d }
}

// WithGradientMax overrides the duration mapped to the end of the elapsed and
// duration gradients on spinner lines (default 20s).
func WithGradientMax(d time.Duration) ConfigOption {
	return func(c *Config) { c.gradientMax = d }
}

// TrashSymbol returns the current trash glyph, already coloured if a colour was
// set, for the sub-packages (e.g. brew) that emit the stray-copy line
// themselves rather than through this package's shared helpers.
func TrashSymbol() string { return cfg.styledTrash() }

// MessageStyle returns the message-text style for the updater's lines, for the
// sub-packages (e.g. brew) that emit a line themselves and want it to match the
// shared helpers' message styling.
func MessageStyle() *lipgloss.Style { return cfg.messageStyle() }

// SetConfig applies opts to the package-level configuration used by Report,
// UpToDate, Spin, and their completion-line variants. Only the fields named by
// opts change; everything unspecified keeps its current value, so a caller
// overrides just what it needs and the rest retain their defaults.
func SetConfig(opts ...ConfigOption) {
	for _, o := range opts {
		o(&cfg)
	}
}
