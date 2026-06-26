package updater

import (
	"cmp"

	"github.com/gechr/clive"
	"github.com/gechr/clive/version"
	"github.com/gechr/clog"
)

// Report logs the result of an update as an old→new pair when the version
// changed, and otherwise defers to [UpToDate]. Both old and current may carry a
// leading "v"; it is stripped before comparison and display.
func Report(name string, info clive.Info, old, current string) {
	old = version.RemovePrefix(old)
	current = version.RemovePrefix(current)
	if old != "" && current != "" && old != current {
		clog.Info().
			Symbol("🎉").
			Str("old", info.VersionLink(old)).
			Str("new", info.VersionLink(current)).
			Msgf("Updated %s", name)
		return
	}
	UpToDate(name, info, cmp.Or(current, old))
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
