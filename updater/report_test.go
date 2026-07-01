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

func TestCompleteReportEmitsUpgradedOutcome(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := completedUpgradeResult(t, &buf)

	require.NoError(t, updater.CompleteReport(res, "App", clive.Info{}, "1.0.7", "1.0.8"))

	require.Equal(t, "INF ⬆️ Upgraded App from=1.0.7 to=1.0.8\n", buf.String())
}

func TestCompleteReportEmitsDowngradedOutcome(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := completedUpgradeResult(t, &buf)

	require.NoError(t, updater.CompleteReport(res, "App", clive.Info{}, "1.0.8", "1.0.7"))

	require.Equal(t, "INF ⬇️ Downgraded App from=1.0.8 to=1.0.7\n", buf.String())
}

func TestCompleteReportEmitsUpToDateOutcome(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := completedUpgradeResult(t, &buf)

	require.NoError(t, updater.CompleteReport(res, "App", clive.Info{}, "1.0.8", "1.0.8"))

	require.Equal(t, "INF 🚀 App is already up-to-date version=1.0.8\n", buf.String())
}

func TestCompleteReportTreatsSemanticEqualAsUpToDate(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := completedUpgradeResult(t, &buf)

	require.NoError(t, updater.CompleteReport(res, "App", clive.Info{}, "1.2", "1.2.0"))

	require.Equal(t, "INF 🚀 App is already up-to-date version=1.2.0\n", buf.String())
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
