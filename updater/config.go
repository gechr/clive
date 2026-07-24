package updater

import (
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
// in updateLinks). A nil colour leaves clog's per-level symbol style to
// govern, which is the default. The set is aligned with a host's own glyph
// set and timing preferences through the With* options and [SetConfig]; it
// is never exposed directly.
type Config struct {
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

	// elapsedMinimum hides a spinner's elapsed field unless the step took at
	// least this long, so quick steps stay uncluttered.
	elapsedMinimum time.Duration
	// gradientMax is the duration mapped to the end of the elapsed and
	// duration gradients on spinner lines.
	gradientMax time.Duration
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
	upToDate:   "🚀",
	upgraded:   "⬆️",
	downgraded: "⬇️",
	done:       "✅",
	trash:      "🗑️",

	trashColor: lipgloss.Color("3"), // yellow by default

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

func (c Config) styledUpToDate() string   { return colored(c.upToDate, c.upToDateColor) }
func (c Config) styledUpgraded() string   { return colored(c.upgraded, c.upgradedColor) }
func (c Config) styledDowngraded() string { return colored(c.downgraded, c.downgradedColor) }
func (c Config) styledDone() string       { return colored(c.done, c.doneColor) }
func (c Config) styledTrash() string      { return colored(c.trash, c.trashColor) }

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

// WithUpToDateSymbol overrides the "already up-to-date" glyph (default 🚀).
func WithUpToDateSymbol(s string) ConfigOption {
	return func(c *Config) { c.upToDate = s }
}

// WithUpgradedSymbol overrides the upgrade glyph (default ⬆️).
func WithUpgradedSymbol(s string) ConfigOption {
	return func(c *Config) { c.upgraded = s }
}

// WithDowngradedSymbol overrides the downgrade glyph (default ⬇️).
func WithDowngradedSymbol(s string) ConfigOption {
	return func(c *Config) { c.downgraded = s }
}

// WithDoneSymbol overrides the completed-spinner-step glyph (default ✅).
func WithDoneSymbol(s string) ConfigOption {
	return func(c *Config) { c.done = s }
}

// WithTrashSymbol overrides the glyph on the line reporting a stray non-Homebrew
// copy being trashed or removed (default 🗑️).
func WithTrashSymbol(s string) ConfigOption {
	return func(c *Config) { c.trash = s }
}

// WithUpToDateColor colours the "already up-to-date" glyph; nil (the default)
// leaves clog's per-level symbol style in charge.
func WithUpToDateColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.upToDateColor = clr }
}

// WithUpgradedColor colours the upgrade glyph; nil (the default) leaves clog's
// per-level symbol style in charge.
func WithUpgradedColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.upgradedColor = clr }
}

// WithDowngradedColor colours the downgrade glyph; nil (the default) leaves
// clog's per-level symbol style in charge.
func WithDowngradedColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.downgradedColor = clr }
}

// WithDoneColor colours the completed-spinner-step glyph; nil (the default)
// leaves clog's per-level symbol style in charge.
func WithDoneColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.doneColor = clr }
}

// WithTrashColor colours the stray-copy glyph; nil (the default) leaves clog's
// per-level symbol style in charge.
func WithTrashColor(clr color.Color) ConfigOption {
	return func(c *Config) { c.trashColor = clr }
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
