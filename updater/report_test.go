package updater_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/gechr/clive"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
	"github.com/gechr/clog/fx"
	"github.com/stretchr/testify/require"
)

func TestCompleteReportEmitsUpdatedOutcome(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := completedUpgradeResult(t, &buf)

	require.NoError(t, updater.CompleteReport(res, "App", clive.Info{}, "1.0.7", "1.0.8"))

	out := buf.String()
	require.Contains(t, out, "Updated App")
	require.Contains(t, out, "from=1.0.7")
	require.Contains(t, out, "to=1.0.8")
	require.NotContains(t, out, "Upgrading App")
	require.NotContains(t, out, "elapsed=")
}

func TestCompleteReportEmitsUpToDateOutcome(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := completedUpgradeResult(t, &buf)

	require.NoError(t, updater.CompleteReport(res, "App", clive.Info{}, "1.0.8", "1.0.8"))

	out := buf.String()
	require.Contains(t, out, "App is already up-to-date")
	require.Contains(t, out, "version=1.0.8")
	require.NotContains(t, out, "Upgrading App")
	require.NotContains(t, out, "elapsed=")
}

func completedUpgradeResult(t *testing.T, buf *bytes.Buffer) *fx.WaitResult {
	t.Helper()

	res := clog.New(clog.TestOutput(buf)).
		Spinner("Upgrading App").
		Elapsed("elapsed").
		NonTTYSilent(true).
		Wait(context.Background(), func(context.Context) error {
			return nil
		})
	require.NoError(t, res.Silent())
	buf.Reset()
	return res
}
