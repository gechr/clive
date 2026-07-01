package updater_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
	"github.com/stretchr/testify/require"
)

// captureDefault redirects the global clog logger to buf for the duration of a
// test, restoring it afterwards. SpinTimeout reports through clog.Default, so a
// test must own that logger to observe (and silence) the line it prints.
func captureDefault(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	l := clog.New(clog.TestOutput(&buf))
	// Pin the elapsed threshold so a sub-second test step never renders a noisy
	// (and nondeterministic) "elapsed=0s", keeping the output exact-matchable.
	f := l.FieldFormats()
	f.ElapsedMinimum = 3 * time.Second
	l.SetFieldFormats(f)
	prev := clog.Default
	clog.Default = l
	t.Cleanup(func() { clog.Default = prev })
	return &buf
}

func TestSpinTimeoutReturnsNilOnSuccess(t *testing.T) {
	buf := captureDefault(t)

	err := updater.SpinTimeout(context.Background(), "Fetching", "Fetched", "Timed out", time.Minute,
		func(context.Context) error { return nil })

	require.NoError(t, err)

	// Success supplants the running "Fetching" label with the done message, so the
	// finished line reads "Fetched" rather than repeating "Fetching".
	require.Equal(t, "INF ⏳ Fetching\nINF ✅ Fetched\n", buf.String())
}

func TestSpinTimeoutSurfacesNonTimeoutError(t *testing.T) {
	captureDefault(t)

	sentinel := errors.New("brew exploded")
	err := updater.SpinTimeout(context.Background(), "Fetching", "Fetched", "Timed out", time.Minute,
		func(context.Context) error { return sentinel })

	// A genuine failure is returned verbatim for the caller to report - it is not
	// masked as a timeout, and it is not the already-reported sentinel.
	require.ErrorIs(t, err, sentinel)
	require.NotErrorIs(t, err, updater.ErrReported)
}

func TestSpinTimeoutSupplantsSpinnerOnTimeout(t *testing.T) {
	buf := captureDefault(t)

	err := updater.SpinTimeout(context.Background(),
		"Fetching latest Clover Homebrew formula",
		"Fetched latest Clover Homebrew formula",
		"Timed out while fetching Clover Homebrew formula",
		10*time.Millisecond,
		func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})

	// The timeout is reported as already-shown, so the caller exits non-zero
	// without logging a second, generic failure line.
	require.ErrorIs(t, err, updater.ErrReported)

	// The spinner's progress line is supplanted with the custom message at error
	// level, its elapsed field swapped for the timeout bound that was hit - no
	// trailing error= field carrying brew's opaque killed-process error, and no
	// second generic failure line.
	require.Equal(t,
		"INF ⏳ Fetching latest Clover Homebrew formula\n"+
			"ERR ❌ Timed out while fetching Clover Homebrew formula timeout=10ms\n",
		buf.String(),
	)
}
