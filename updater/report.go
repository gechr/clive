package updater

import (
	"cmp"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clive"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
	"github.com/gechr/clog/fx"
)

// Report logs the result of an update as an old→new pair when the version
// changed, and otherwise defers to [UpToDate]. Both old and current may carry a
// leading "v"; it is stripped before comparison and display.
func Report(name string, info clive.Info, old, current string) {
	old = version.RemovePrefix(old)
	current = version.RemovePrefix(current)
	if old != "" && current != "" && old != current {
		from, to := updateLinks(info, old, current)
		clog.Info().
			Symbol("🎉").
			Str("from", from).
			Str("to", to).
			Msgf("Updated %s", name)
		return
	}
	UpToDate(name, info, cmp.Or(current, old))
}

// CompleteReport emits the update result as the completion line for a spinner
// result, replacing that spinner's transient progress line in TTY output.
func CompleteReport(res *fx.WaitResult, name string, info clive.Info, old, current string) error {
	old = version.RemovePrefix(old)
	current = version.RemovePrefix(current)
	res.Fields = nil
	if old != "" && current != "" && old != current {
		from, to := updateLinks(info, old, current)
		return res.
			Symbol("🎉").
			Str("from", from).
			Str("to", to).
			Msgf("Updated %s", name)
	}
	return CompleteUpToDate(res, name, info, cmp.Or(current, old))
}

// UpToDate warns that no update was applied, including the version field only
// when a version is known (a go-run build has none to show).
func UpToDate(name string, info clive.Info, ver string) {
	e := clog.Warn()
	if ver != "" {
		e = e.Str("version", info.VersionLink(ver))
	}
	e.Msgf("%s is already up-to-date", name)
}

// CompleteUpToDate emits an up-to-date warning as a spinner completion line.
func CompleteUpToDate(res *fx.WaitResult, name string, info clive.Info, ver string) error {
	res = res.OnSuccessLevel(clog.LevelWarn)
	if ver != "" {
		res = res.Str("version", info.VersionLink(ver))
	}
	return res.Msgf("%s is already up-to-date", name)
}

func updateLinks(info clive.Info, old, current string) (string, string) {
	from := info.VersionLink(old)
	to := info.VersionLink(current)
	if !clog.ColorsDisabled() {
		from = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(from) // red
		to = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(to)     // green
	}
	return from, to
}
