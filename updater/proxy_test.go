package updater_test

import (
	"testing"

	"github.com/gechr/clive/updater"
	"github.com/stretchr/testify/require"
)

func TestProxyBypass(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"HTTP_PROXY=",
		"http_proxy=",
		"HTTPS_PROXY=",
		"https_proxy=",
		"ALL_PROXY=",
		"all_proxy=",
		"NO_PROXY=*",
		"no_proxy=*",
	}, updater.ProxyBypass())
}

func TestGoPrivate(t *testing.T) {
	t.Parallel()

	const module = "github.com/gechr/clive"
	require.Equal(
		t,
		module,
		updater.GoPrivate(module, ""),
		"an empty existing value yields just the module",
	)
	require.Equal(
		t,
		module+",example.com/*",
		updater.GoPrivate(module, "example.com/*"),
		"an existing value is preserved after the module",
	)
	require.Equal(t, module, updater.GoPrivate("  "+module+"  ", "  "), "both inputs are trimmed")
}
