package updater

import (
	"cmp"
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
)

// DefaultHintColor is the orange accent for the update hint message.
var DefaultHintColor color.Color = lipgloss.Color("208")

// OutdatedHint renders the shared "you're behind" update hint. Build one with
// [NewOutdatedHint] and options, then call [OutdatedHint.Log].
type OutdatedHint struct {
	accent         color.Color
	command        string
	installedColor color.Color
	latestColor    color.Color
	symbol         string
}

// HintOption configures an [OutdatedHint].
type HintOption func(*OutdatedHint)

// WithOutdatedHintSymbol overrides the hint's leading glyph (default 💡).
func WithOutdatedHintSymbol(symbol string) HintOption {
	return func(h *OutdatedHint) { h.symbol = symbol }
}

// WithOutdatedHintCommand overrides the command the hint tells the user to run
// (default "<binary> update"). Use this for a tool whose self-update is invoked
// differently, e.g. a flag-only grammar's "<binary> --self-update". An empty
// command is ignored.
func WithOutdatedHintCommand(command string) HintOption {
	return func(h *OutdatedHint) {
		if command != "" {
			h.command = command
		}
	}
}

// WithOutdatedHintColor overrides the message accent colour (default orange). A
// nil colour is ignored.
func WithOutdatedHintColor(c color.Color) HintOption {
	return func(h *OutdatedHint) {
		if c != nil {
			h.accent = c
		}
	}
}

// NewOutdatedHint builds a hint with defaults - 💡, an orange message, a red
// installed version and a green latest version - then applies opts.
func NewOutdatedHint(opts ...HintOption) OutdatedHint {
	h := OutdatedHint{
		symbol:         "💡",
		accent:         DefaultHintColor,
		installedColor: lipgloss.Color("1"), // red
		latestColor:    lipgloss.Color("2"), // green
	}
	for _, o := range opts {
		o(&h)
	}
	return h
}

// Log renders the hint: the symbol, the installed (red) and latest (green)
// versions as fields, and a coloured "<name> is outdated - run `<binary> update`
// to upgrade!" message with the command emphasised. The command defaults to
// "<binary> update" and is overridden with [WithOutdatedHintCommand] for a tool
// whose self-update is invoked differently. The colours are applied to the
// values here rather than via global clog styles, so a host's own field
// formatting is never affected. installed and latest are already display-formatted.
func (h OutdatedHint) Log(displayName, binaryName, installed, latest string) {
	command := cmp.Or(h.command, binaryName+" update")
	msg := displayName + " is outdated - run '" + command + "' to upgrade!"
	if !clog.ColorsDisabled() {
		style := lipgloss.NewStyle().Foreground(h.accent)
		msg = style.Render(displayName+" is outdated - run '") +
			style.Bold(true).Render(command) +
			style.Render("' to upgrade!")
		installed = lipgloss.NewStyle().Foreground(h.installedColor).Render(installed)
		latest = lipgloss.NewStyle().Foreground(h.latestColor).Render(latest)
	}
	clog.Warn().
		Symbol(h.symbol).
		Str("installed", installed).
		Str("latest", latest).
		Msg(msg)
}

// HintFor logs the update hint for a tool, stripping a leading "v" and formatting
// each version through the tool's VersionLink. Options customize the presentation
// (e.g. [WithOutdatedHintSymbol]).
func HintFor(tool Tool, current, latest string, opts ...HintOption) {
	NewOutdatedHint(opts...).Log(
		tool.DisplayName(),
		tool.BinaryName(),
		tool.VersionLink(version.RemovePrefix(current)),
		tool.VersionLink(version.RemovePrefix(latest)),
	)
}
