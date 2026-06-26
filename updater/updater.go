// Package updater holds the shared surface for clive's self-update mechanisms.
// Each mechanism is a sibling subpackage - brew (Homebrew), goinstall (`go
// install`), and github (release assets) - that describes a tool with its own
// Config and exposes Check/Update. This package owns the pieces those Configs
// have in common: the [Tool] interface that consumers such as notify depend on,
// and the cross-mechanism UX helpers ([Report], [UpToDate], [Spin]), install
// directory resolution ([InstallDir]), and subprocess-environment helpers
// ([ProxyBypass], [GoPrivate]).
//
// It deliberately does not define a behavioural Updater interface over
// Check/Update: each mechanism's Channel enum differs, and nothing drives an
// updater polymorphically. The clog dependency lives here and in the
// subpackages, keeping the core clive package dependency-light.
package updater

import (
	"context"
	"net/http"
)

// Tool is the metadata a consumer needs to describe a self-updating CLI without
// depending on how it updates. The brew, goinstall, and github Configs each
// satisfy it. VersionLink and LatestRef are methods rather than an Info()
// accessor because Config carries Info as a field, and a struct cannot expose
// both a field Info and a method Info.
type Tool interface {
	// BinaryName is the executable/command name, e.g. "myapp".
	BinaryName() string
	// DisplayName is the human-facing name shown in messages.
	DisplayName() string
	// VersionLink renders v as a clickable link to its release/commit.
	VersionLink(v string) string
	// LatestRef returns the newest installable ref of the tool, fetched over the
	// network without a toolchain so a distributed binary can call it. Each
	// mechanism answers in its own currency: brew and goinstall report the
	// highest semver tag in the repository, while github - which installs release
	// assets - reports the latest release's tag, so a consumer's "update
	// available" signal matches what the updater can actually fetch. A nil client
	// uses [http.DefaultClient].
	LatestRef(ctx context.Context, client *http.Client) (string, error)
}
