package goinstall_test

import (
	"testing"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater/goinstall"
	"github.com/stretchr/testify/require"
)

func TestChannelFor(t *testing.T) {
	t.Parallel()

	require.Equal(t, goinstall.Latest, goinstall.ChannelFor(false), "unset is latest")
	require.Equal(t, goinstall.Dev, goinstall.ChannelFor(true), "dev branches off")
}

func TestBinaryDefaultsToModuleBase(t *testing.T) {
	t.Parallel()

	cfg := goinstall.Config{Info: clive.Info{Module: "github.com/owner/example"}}
	require.Equal(t, "example", cfg.BinaryName(), "defaults to the last module element")

	cfg.Binary = "clv"
	require.Equal(t, "clv", cfg.BinaryName(), "an explicit binary wins")
}

func TestDisplayNameDefaultsToBinary(t *testing.T) {
	t.Parallel()

	cfg := goinstall.Config{Info: clive.Info{Module: "github.com/owner/example"}}
	require.Equal(t, "example", cfg.DisplayName(), "defaults to the binary name")

	cfg.Name = "Example"
	require.Equal(t, "Example", cfg.DisplayName(), "an explicit name wins")
}

func TestInstallTarget(t *testing.T) {
	t.Parallel()

	cfg := goinstall.Config{Info: clive.Info{Module: "github.com/owner/example"}}
	require.Equal(
		t,
		"github.com/owner/example@latest",
		cfg.InstallTarget(goinstall.Latest),
		"the stable channel installs @latest",
	)
	require.Equal(
		t,
		"github.com/owner/example@main",
		cfg.InstallTarget(goinstall.Dev),
		"the dev channel defaults to @main",
	)

	cfg.Branch = "develop"
	require.Equal(
		t,
		"github.com/owner/example@develop",
		cfg.InstallTarget(goinstall.Dev),
		"an explicit branch is honoured",
	)
}

func TestModuleVersion(t *testing.T) {
	t.Parallel()

	const out = "/home/u/.local/bin/example: go1.26.2\n" +
		"\tpath\tgithub.com/owner/example\n" +
		"\tmod\tgithub.com/owner/example\tv0.32.0\th1:abc=\n" +
		"\tdep\tgithub.com/spf13/cobra\tv1.8.0\th1:def=\n"
	require.Equal(
		t,
		"v0.32.0",
		goinstall.ModuleVersion([]byte(out)),
		"the mod line carries the main module's version",
	)

	require.Empty(
		t,
		goinstall.ModuleVersion([]byte("example: go1.26.2\n")),
		"a binary without module info reports no version",
	)
	require.Empty(t, goinstall.ModuleVersion(nil))
}
