package clive_test

import (
	"testing"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/assert"
)

func TestVersionLinkNoRepo(t *testing.T) {
	t.Parallel()

	// With no Module/Repo, VersionLink returns the input unchanged.
	i := clive.Info{}
	assert.Equal(t, "v1.2.3", i.VersionLink("v1.2.3"))
	assert.Empty(t, i.VersionLink(""))
}

func TestVersionLinkPlainTextInTests(t *testing.T) {
	t.Parallel()

	// `go test` runs without a TTY, so clog disables hyperlink emission and
	// VersionLink returns the raw version string verbatim.
	i := clive.Info{Module: "github.com/gechr/clive"}

	for _, v := range []string{
		"v1.2.3",
		"1.2.3",
		"v0.21.4-1-g4bed8a3-dev",
		"v0.21.4-4bed8a3-dev",
	} {
		assert.Equalf(t, v, i.VersionLink(v), "VersionLink(%q)", v)
	}
}

func TestVersionLinkEmptyForUnknownRepo(t *testing.T) {
	t.Parallel()

	// A non-github module path with no explicit Repo yields no hyperlink:
	// the version comes back verbatim.
	i := clive.Info{Module: "go.example.com/foo"}
	got := i.VersionLink("v1.2.3")
	// No OSC8 escape sequence (\x1b]8) should appear.
	assert.NotContainsf(t, got, "\x1b]8",
		"unexpected hyperlink for non-github module: %q", got)
	assert.Equal(t, "v1.2.3", got)
}
