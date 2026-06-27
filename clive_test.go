package clive_test

import (
	"context"
	"testing"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateAvailableNoModule(t *testing.T) {
	t.Parallel()

	// Info with no Module cannot query the proxy.
	i := clive.Info{}
	_, err := i.UpdateAvailable(context.Background())
	require.Error(t, err)
}

func TestVersionURL(t *testing.T) {
	t.Parallel()

	i := clive.Info{Module: "github.com/gechr/clive"}
	tests := map[string]string{
		"v1.2.3":                 "https://github.com/gechr/clive/releases/tag/v1.2.3",
		"1.2.3":                  "https://github.com/gechr/clive/releases/tag/v1.2.3",
		"v0.21.4-1-g4bed8a3-dev": "https://github.com/gechr/clive/commit/4bed8a3",
		"v0.21.4-4bed8a3-dev":    "https://github.com/gechr/clive/commit/4bed8a3",
	}
	for v, want := range tests {
		assert.Equalf(t, want, i.VersionURL(v), "VersionURL(%q)", v)
	}
}

func TestVersionURLNoRepo(t *testing.T) {
	t.Parallel()

	// No Module/Repo, an empty version, and a non-github module all yield no URL.
	assert.Empty(t, clive.Info{}.VersionURL("v1.2.3"))
	assert.Empty(t, clive.Info{Module: "github.com/gechr/clive"}.VersionURL(""))
	assert.Empty(t, clive.Info{Module: "go.example.com/foo"}.VersionURL("v1.2.3"))
}

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
		"v1.2.3", "1.2.3", "v0.21.4-1-g4bed8a3-dev", "v0.21.4-4bed8a3-dev",
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
	assert.NotContainsf(t, got, "\x1b]8", "unexpected hyperlink for non-github module: %q", got)
	assert.Equal(t, "v1.2.3", got)
}
