// Package updater holds the shared surface for clive's self-update mechanisms.
// Each mechanism is a sibling subpackage - brew (Homebrew), goinstall (`go
// install`), and github (release assets) - that describes a tool with its own
// Config and exposes Check/Update. This package owns the pieces those Configs
// have in common: the [Tool] interface that consumers such as notify depend on,
// and the cross-mechanism UX helpers ([Report], [UpToDate], [Spin]), install
// directory resolution ([InstallDir]), and subprocess-environment helpers
// ([ProxyBypass], [GoPrivate]).
//
// It also defines the behavioural [Updater] interface, satisfied by every
// mechanism's Config, so a consumer can drive self-updates without knowing the
// install method; the dev/stable flag pair is the common channel currency each
// mechanism maps onto its own Channel enum. The clog dependency lives here and
// in the subpackages, keeping the core clive package dependency-light.
package updater

import (
	"context"
	"net/http"
)

// Updater is a [Tool] that can also check for and install updates of itself.
// The dev/stable pair maps onto each mechanism's own channels: brew installs
// the latest tagged release for stable and a source build for dev; goinstall
// treats dev as the tip of the dev branch; github treats dev as the newest
// prerelease. Neither set selects the mechanism's default channel, and stable
// is meaningful only to brew (the other mechanisms' defaults already are the
// latest stable release).
type Updater interface {
	Tool
	// Check reports whether an update is available without installing it.
	Check(ctx context.Context) error
	// Update installs the latest release from the selected channel.
	Update(ctx context.Context, dev, stable bool) error
}

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
