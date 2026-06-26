package updater

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clog"
)

// DefaultHintColor is the orange accent for the update hint.
var DefaultHintColor color.Color = lipgloss.Color("208")

// Hint logs the shared "you're behind" update hint: the 💡 symbol, the installed
// and latest refs as fields, and a "<name> is outdated! Run `<binary> update` to
// upgrade" message with the command emphasised in accent. It is the single
// renderer used by every mechanism's Check and by notify, so the active and
// passive update paths read identically. installed and latest are already
// display-formatted.
func Hint(displayName, binaryName, installed, latest string, accent color.Color) {
	command := binaryName + " update"
	msg := displayName + " is outdated! Run '" + command + "' to upgrade"
	if !clog.ColorsDisabled() {
		style := lipgloss.NewStyle().Foreground(accent)
		msg = style.Render(displayName+" is outdated! Run '") +
			style.Bold(true).Render(command) +
			style.Render("' to upgrade")
	}
	clog.Warn().
		Symbol("💡").
		Str("installed", installed).
		Str("latest", latest).
		Msg(msg)
}

// HintFor logs the default-styled update hint for a tool, formatting current and
// latest through the tool's VersionLink. It is the convenience each Check uses.
func HintFor(tool Tool, current, latest string) {
	Hint(
		tool.DisplayName(),
		tool.BinaryName(),
		tool.VersionLink(current),
		tool.VersionLink(latest),
		DefaultHintColor,
	)
}
