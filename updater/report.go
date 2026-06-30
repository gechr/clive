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
	if symbol, verb, changed := outcome(old, current); changed {
		from, to := updateLinks(info, old, current)
		clog.Info().
			Symbol(symbol).
			Str("from", from).
			Str("to", to).
			Msgf("%s %s", verb, name)
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
	if symbol, verb, changed := outcome(old, current); changed {
		from, to := updateLinks(info, old, current)
		return res.
			Symbol(symbol).
			Str("from", from).
			Str("to", to).
			Msgf("%s %s", verb, name)
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

// outcome picks the log symbol and verb for a version change, distinguishing an
// upgrade from a downgrade so a backwards move (e.g. a shadowing copy reporting a
// higher "from" than the freshly-installed release) is not mislabelled. The final
// return is false when either version is empty or the two are semantically equal
// (so "1.2" and "1.2.0", or a dev build and its base, are not reported as a
// change), letting the caller fall back to an up-to-date report.
func outcome(old, current string) (string, string, bool) {
	if old == "" || current == "" {
		return "", "", false
	}
	switch c := version.CompareString(current, old); {
	case c < 0:
		return "⬇️", "Downgraded", true
	case c > 0:
		return "⬆️", "Upgraded", true
	default:
		return "", "", false
	}
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
