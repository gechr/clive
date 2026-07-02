package brew_test

import (
	"testing"

	"github.com/gechr/clive"
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

func TestConflictPolicyZeroValueWarns(t *testing.T) {
	t.Parallel()

	// The zero value must default to warning, so a Config that does not set an
	// on-conflict policy leaves stray installs in place but flags them.
	require.Equal(t, brew.ConflictWarn, brew.ConflictPolicy(0))
}

func TestBinaryDefaultsToFormula(t *testing.T) {
	t.Parallel()

	require.Equal(t, "clover", brew.New(clive.Info{}, brew.WithFormula("clover")).BinaryName())
	require.Equal(
		t,
		"clover",
		brew.New(clive.Info{Module: "github.com/gechr/clover"}).BinaryName(),
		"the formula (and thus binary) infers from the module path",
	)
	require.Equal(
		t,
		"clv",
		brew.New(clive.Info{}, brew.WithFormula("clover"), brew.WithBinary("clv")).BinaryName(),
	)
}

func TestFormulaRef(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"clover",
		brew.New(clive.Info{}, brew.WithFormula("clover")).FormulaRef(),
		"no tap",
	)
	require.Equal(
		t,
		"gechr/tap/clover",
		brew.New(clive.Info{}, brew.WithFormula("clover"), brew.WithTap("gechr/tap")).FormulaRef(),
		"tap-qualified",
	)
}

func TestHeadBuildMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    bool
	}{
		{
			"0.1.0-gabc1234-dev",
			true,
		},
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
