package github

import (
	"context"
	"errors"
	"testing"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater/github/internal/installer"
	"github.com/stretchr/testify/require"
)

// withResolve stubs release discovery for a test and restores it on cleanup.
func withResolve(
	t *testing.T,
	fn func(context.Context, Config, bool) (*installer.Updater, *installer.Release, bool, error),
) {
	t.Helper()
	prev := resolve
	resolve = fn
	t.Cleanup(func() { resolve = prev })
}

// withCurrent pins currentVersion for a test and restores it on cleanup.
func withCurrent(t *testing.T, v string) {
	t.Helper()
	prev := currentVersion
	currentVersion = func() string { return v }
	t.Cleanup(func() { currentVersion = prev })
}

func notFound(
	context.Context,
	Config,
	bool,
) (*installer.Updater, *installer.Release, bool, error) {
	return nil, nil, false, nil
}

func TestChannelFor(t *testing.T) {
	t.Parallel()

	require.Equal(t, Latest, ChannelFor(false))
	require.Equal(t, Prerelease, ChannelFor(true))
}

func TestBinaryName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "clive", Config{binary: "clive"}.BinaryName(), "explicit binary wins")
	require.Equal(
		t,
		"clover",
		Config{info: clive.Info{Repo: "gechr/clover"}}.BinaryName(),
		"defaults to the repo name",
	)
	require.Equal(
		t,
		"myapp",
		Config{info: clive.Info{Module: "github.com/gechr/myapp"}}.BinaryName(),
		"derives from a github.com module",
	)
}

func TestDisplayName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "clive", Config{binary: "clive"}.DisplayName(), "defaults to the binary name")
	require.Equal(t, "Clive", Config{name: "Clive", binary: "clive"}.DisplayName())
}

func TestRepo(t *testing.T) {
	t.Parallel()

	owner, name, err := repo(clive.Info{Repo: "gechr/clover"})
	require.NoError(t, err)
	require.Equal(t, "gechr", owner)
	require.Equal(t, "clover", name)

	owner, name, err = repo(clive.Info{Module: "github.com/gechr/clive"})
	require.NoError(t, err)
	require.Equal(t, "gechr", owner)
	require.Equal(t, "clive", name)

	_, dropped, err := repo(clive.Info{Module: "github.com/gechr/clive/v2"})
	require.NoError(t, err)
	require.Equal(t, "clive", dropped, "a module major-version suffix is dropped")

	_, _, err = repo(clive.Info{Module: "example.com/gechr/clive"})
	require.Error(t, err, "a non-github module has no derivable repo")
}

func TestResolveTokenEnvOverride(t *testing.T) {
	// Not parallel: mutates the environment.
	t.Setenv("CLIVE_TEST_TOKEN", "from-env")
	require.Equal(
		t,
		"from-env",
		resolveToken(Config{tokenEnv: "CLIVE_TEST_TOKEN"}),
		"the configured env var beats any gh credential",
	)
}

func TestTokenHost(t *testing.T) {
	t.Parallel()

	require.Equal(t, "github.com", tokenHost(Config{}), "defaults to github.com")
	require.Equal(
		t,
		"ghe.example.com",
		tokenHost(Config{enterpriseURL: "https://ghe.example.com/api/v3/"}),
		"derives the host from an Enterprise API URL",
	)
	require.Equal(
		t,
		"github.com",
		tokenHost(Config{enterpriseURL: "://bad"}),
		"falls back to github.com when the URL has no host",
	)
}

func TestIsNewer(t *testing.T) {
	t.Parallel()

	require.True(t, isNewer("v1.0.0", "v1.0.1"))
	require.True(t, isNewer("1.0.0", "v2.0.0"), "a leading v is optional")
	require.False(t, isNewer("v1.2.0", "v1.2.0"), "equal is not newer")
	require.False(t, isNewer("v1.2.0", "v1.1.0"))
	require.False(t, isNewer("v1.0.0", "not-a-version"))
}

func TestCheckUpToDateWhenNotFound(t *testing.T) {
	// Not parallel: stubs the package-level resolve seam.
	withResolve(t, notFound)
	require.NoError(
		t,
		Check(context.Background(), Config{info: clive.Info{Repo: "gechr/clive"}}),
		"no release found reports up-to-date, not an error",
	)
}

func TestCheckPropagatesError(t *testing.T) {
	// Not parallel: stubs the package-level resolve seam.
	boom := errors.New("rate limited")
	withResolve(
		t,
		func(context.Context, Config, bool) (*installer.Updater, *installer.Release, bool, error) {
			return nil, nil, false, boom
		},
	)
	err := Check(context.Background(), Config{info: clive.Info{Repo: "gechr/clive"}})
	require.ErrorIs(t, err, boom)
	require.ErrorContains(t, err, "check for updates")
}

func TestUpdateNoOpWhenNotFound(t *testing.T) {
	// Not parallel: stubs the package-level resolve and currentVersion seams.
	withCurrent(t, "v1.0.0")
	withResolve(t, notFound)
	require.NoError(
		t,
		Update(context.Background(), Config{info: clive.Info{Repo: "gechr/clive"}}, Latest),
		"no release found is a clean no-op",
	)
}

func TestLatestRefNotFound(t *testing.T) {
	// Not parallel: stubs the package-level resolve seam.
	withResolve(t, notFound)
	ref, err := Config{info: clive.Info{Repo: "gechr/clive"}}.LatestRef(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, ref)
}
