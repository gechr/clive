package brew_test

import (
	"testing"

	"github.com/gechr/clive/updater/brew"
	"github.com/stretchr/testify/require"
)

func TestChannelFor(t *testing.T) {
	t.Parallel()

	require.Equal(t, brew.Upgrade, brew.ChannelFor(false, false), "neither flag is upgrade")
	require.Equal(t, brew.Dev, brew.ChannelFor(true, false), "dev wins")
	require.Equal(t, brew.Stable, brew.ChannelFor(false, true), "stable")
	require.Equal(t, brew.Dev, brew.ChannelFor(true, true), "dev takes precedence")
}

func TestLinkedKeg(t *testing.T) {
	t.Parallel()

	const multi = `{"formulae":[{"linked_keg":"0.32.0",` +
		`"installed":[{"version":"0.31.7"},{"version":"0.32.0"}]}]}`
	require.Equal(t, "0.32.0", brew.LinkedKeg([]byte(multi)),
		"the linked keg wins over the arbitrary order of installed kegs")

	require.Empty(t, brew.LinkedKeg([]byte(`{"formulae":[{"linked_keg":""}]}`)),
		"an unlinked formula reports no version")
	require.Empty(t, brew.LinkedKeg([]byte(`{"formulae":[]}`)))
	require.Empty(t, brew.LinkedKeg([]byte("not json")))
}

func TestConflictPolicyZeroValueWarns(t *testing.T) {
	t.Parallel()

	// The zero value must default to warning, so a Config that does not set
	// OnConflict leaves stray installs in place but flags them.
	require.Equal(t, brew.ConflictWarn, brew.Config{}.OnConflict)
}

func TestBinaryDefaultsToFormula(t *testing.T) {
	t.Parallel()

	require.Equal(t, "clover", brew.Config{Formula: "clover"}.BinaryName())
	require.Equal(t, "clv", brew.Config{Formula: "clover", Binary: "clv"}.BinaryName())
}

func TestFormulaRef(t *testing.T) {
	t.Parallel()

	require.Equal(t, "clover", brew.Config{Formula: "clover"}.FormulaRef(), "no tap")
	require.Equal(t,
		"gechr/tap/clover",
		brew.Config{Formula: "clover", Tap: "gechr/tap"}.FormulaRef(),
		"tap-qualified",
	)
}

func TestHeadBuildMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    bool
	}{
		{"0.1.0-gabc1234-dev", true},
		{"0.1.0-deadbeef-dev", true},
		{"v0.0.0-g0abc123-dev", true},
		{"0.1.0", false},
		{"v1.2.3", false},
		{"1.2.3-rc.1", false},
	}
	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, brew.HeadBuild.MatchString(tc.version))
		})
	}
}
