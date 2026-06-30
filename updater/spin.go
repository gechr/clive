package updater

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gechr/clog"
	"github.com/gechr/clog/fx"
)

const (
	// doneSymbol marks a completed spinner step, replacing the default info glyph.
	doneSymbol = "✅"
	// elapsedMinimum hides a spinner's elapsed field unless the step took at least
	// this long, so quick steps stay uncluttered.
	elapsedMinimum = 3 * time.Second
)

// ErrReported marks a failure that has already been shown to the user - a spinner
// step finalized at error level (e.g. by [SpinTimeout]) - so its message is on
// screen. A consumer should treat it as a plain non-zero exit and not log a
// second, generic failure line on top of the one already printed.
var ErrReported = errors.New("update failed")

// elapsedOnce applies elapsedMinimum to the default logger the first time a
// spinner runs, so a quick step does not print a noisy "elapsed=0s".
var elapsedOnce sync.Once

// Field is an optional structured key/value attached to a [Spin] message, shown
// on both the spinner and its completion line (e.g. version="1.2.3").
type Field struct {
	Key string
	Val string
}

// Spin runs fn under a clog spinner labelled msg, with any fields attached: on
// success it logs a completion line, and on failure it returns fn's error
// without the spinner logging its own error line, so the caller reports the
// failure exactly once.
func Spin(ctx context.Context, msg string, fn func(context.Context) error, fields ...Field) error {
	res := SpinResult(ctx, msg, fn, fields...)
	if err := res.Silent(); err != nil {
		return err
	}
	return res.Symbol(doneSymbol).Msg(msg)
}

// SpinTimeout runs fn under a spinner like [Spin], but bounds it with timeout.
// On a clean timeout it supplants the spinner with timeoutMsg at error level,
// swapping the spinner's elapsed field for a timeout one naming the bound that
// was hit, so the line reads e.g. "Timed out ... timeout=2m" rather than
// clearing or surfacing the killed subprocess's opaque error - and returns
// [ErrReported] so the caller can exit non-zero without a consumer
// double-reporting. Any other failure behaves like [Spin] (silent spinner, fn's
// error returned for the caller to report); success logs the normal completion
// line (with elapsed). A context cancellation (e.g. Ctrl-C) is not a timeout and
// falls through to the silent path.
func SpinTimeout(
	ctx context.Context,
	msg, timeoutMsg string,
	timeout time.Duration,
	fn func(context.Context) error,
) error {
	// Bound the task - not the spinner - with the timeout. If the timeout
	// cancelled the spinner's own context, the spinner would treat it as an
	// interrupt and freeze its last frame as a committed line, printing the
	// timeout message below "Fetching ..." instead of replacing it. Timing out
	// only the task lets the spinner finalize through its normal completion path,
	// erasing its line so Send can supplant it in place.
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := SpinResult(ctx, msg, func(context.Context) error { return fn(taskCtx) })
	if err := res.Silent(); err != nil {
		if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			res.TaskErr = errors.New(timeoutMsg)
			// Replace the spinner's elapsed field with the timeout bound that was
			// hit - "timeout=2m" reads clearer than "elapsed=2m" on a timeout line.
			res.Fields = nil
			res.Duration("timeout", timeout)
			// Wrap the timeout message with ErrReported so a consumer can detect
			// the already-reported failure yet still unwrap the detail.
			return fmt.Errorf("%w: %w", ErrReported, res.Send())
		}
		return err
	}
	return res.Symbol(doneSymbol).Msg(msg)
}

// SpinResult runs fn under a clog spinner labelled msg and returns the
// unfinalized result so callers can choose the completion line.
func SpinResult(
	ctx context.Context,
	msg string,
	fn func(context.Context) error,
	fields ...Field,
) *fx.WaitResult {
	elapsedOnce.Do(func() {
		f := clog.Default.FieldFormats()
		f.ElapsedMinimum = elapsedMinimum
		clog.Default.SetFieldFormats(f)
	})

	b := clog.Spinner(msg)
	for _, f := range fields {
		b = b.Str(f.Key, f.Val)
	}
	return b.Elapsed("elapsed").Wait(ctx, fn)
}

// TransientSpinResult is like [SpinResult], but suppresses the non-TTY progress
// line so only the caller's final completion message is written to scrollback.
func TransientSpinResult(
	ctx context.Context,
	msg string,
	fn func(context.Context) error,
	fields ...Field,
) *fx.WaitResult {
	elapsedOnce.Do(func() {
		f := clog.Default.FieldFormats()
		f.ElapsedMinimum = elapsedMinimum
		clog.Default.SetFieldFormats(f)
	})

	b := clog.Spinner(msg).NonTTYSilent(true)
	for _, f := range fields {
		b = b.Str(f.Key, f.Val)
	}
	return b.Elapsed("elapsed").Wait(ctx, fn)
}
