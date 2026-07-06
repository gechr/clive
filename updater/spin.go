package updater

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gechr/clog"
	"github.com/gechr/clog/field/duration"
	"github.com/gechr/clog/field/elapsed"
	"github.com/gechr/clog/fx"
)

// ErrReported marks a failure that has already been shown to the user - a spinner
// step finalized at error level (e.g. by [SpinTimeout]) - so its message is on
// screen. A consumer should treat it as a plain non-zero exit and not log a
// second, generic failure line on top of the one already printed.
var ErrReported = errors.New("update failed")

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
	return SpinProgress(
		ctx, msg,
		func(ctx context.Context, _ *fx.Update) error { return fn(ctx) },
		fields...,
	)
}

// SpinProgress is [Spin] where fn additionally receives the spinner's live
// [fx.Update], letting it revise the running label mid-flight (e.g. switch to a
// "Waiting ..." message while it blocks). The success completion line still uses
// msg, so a temporary relabel does not change what is committed to scrollback.
func SpinProgress(
	ctx context.Context,
	msg string,
	fn func(context.Context, *fx.Update) error,
	fields ...Field,
) error {
	res := spinResultProgress(ctx, msg, false, fn, fields...)
	if err := res.Silent(); err != nil {
		return err
	}
	return res.Symbol(cfg.styledDone()).MessageStyle(cfg.messageStyle()).Msg(msg)
}

// SpinTimeout runs fn under a spinner like [Spin], but bounds it with timeout.
// On a clean timeout it supplants the spinner with timeoutMsg at error level,
// swapping the spinner's elapsed field for a timeout one naming the bound that
// was hit, so the line reads e.g. "Timed out ... timeout=2m" rather than
// clearing or surfacing the killed subprocess's opaque error - and returns
// [ErrReported] so the caller can exit non-zero without a consumer
// double-reporting. Any other failure behaves like [Spin] (silent spinner, fn's
// error returned for the caller to report); success supplants the spinner with
// doneMsg (with elapsed), so the finished line can read "Fetched ..." in place of
// the running "Fetching ..." label. A context cancellation (e.g. Ctrl-C) is not a
// timeout and falls through to the silent path.
func SpinTimeout(
	ctx context.Context,
	msg, doneMsg, timeoutMsg string,
	timeout time.Duration,
	fn func(context.Context) error,
) error {
	return SpinTimeoutProgress(
		ctx, msg, doneMsg, timeoutMsg, timeout,
		func(ctx context.Context, _ *fx.Update) error { return fn(ctx) },
	)
}

// SpinTimeoutProgress is [SpinTimeout] where fn is additionally handed the
// spinner's live [fx.Update], letting it revise the running label mid-flight -
// e.g. to switch "Fetching ..." to "Waiting for another process ..." while it
// blocks - without disturbing the timeout, done, and error handling.
func SpinTimeoutProgress(
	ctx context.Context,
	msg, doneMsg, timeoutMsg string,
	timeout time.Duration,
	fn func(context.Context, *fx.Update) error,
) error {
	// Bound the task - not the spinner - with the timeout. If the timeout
	// cancelled the spinner's own context, the spinner would treat it as an
	// interrupt and freeze its last frame as a committed line, printing the
	// timeout message below "Fetching ..." instead of replacing it. Timing out
	// only the task lets the spinner finalize through its normal completion path,
	// erasing its line so Send can supplant it in place.
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := spinResultProgress(ctx, msg, false, func(_ context.Context, u *fx.Update) error {
		return fn(taskCtx, u)
	})
	if err := res.Silent(); err != nil {
		if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			res.TaskErr = errors.New(timeoutMsg)
			// Replace the spinner's elapsed field with the timeout bound that was
			// hit - "timeout=2m" reads clearer than "elapsed=2m" on a timeout line.
			res.Fields = nil
			res.Duration("timeout", timeout, duration.WithGradientMax(cfg.gradientMax))
			// Wrap the timeout message with ErrReported so a consumer can detect
			// the already-reported failure yet still unwrap the detail.
			return fmt.Errorf("%w: %w", ErrReported, res.Send())
		}
		return err
	}
	return res.Symbol(cfg.styledDone()).MessageStyle(cfg.messageStyle()).Msg(doneMsg)
}

// SpinResult runs fn under a clog spinner labelled msg and returns the
// unfinalized result so callers can choose the completion line.
func SpinResult(
	ctx context.Context,
	msg string,
	fn func(context.Context) error,
	fields ...Field,
) *fx.WaitResult {
	return spinResultProgress(
		ctx, msg, false,
		func(ctx context.Context, _ *fx.Update) error { return fn(ctx) },
		fields...,
	)
}

// TransientSpinResult is like [SpinResult], but suppresses the non-TTY progress
// line so only the caller's final completion message is written to scrollback.
func TransientSpinResult(
	ctx context.Context,
	msg string,
	fn func(context.Context) error,
	fields ...Field,
) *fx.WaitResult {
	return spinResultProgress(
		ctx, msg, true,
		func(ctx context.Context, _ *fx.Update) error { return fn(ctx) },
		fields...,
	)
}

// TransientSpinResultProgress is [TransientSpinResult] where fn additionally
// receives the spinner's live [fx.Update] to revise the running label mid-flight.
func TransientSpinResultProgress(
	ctx context.Context,
	msg string,
	fn func(context.Context, *fx.Update) error,
	fields ...Field,
) *fx.WaitResult {
	return spinResultProgress(ctx, msg, true, fn, fields...)
}

// spinResultProgress is the shared core of the Spin* helpers: it runs fn under a
// clog spinner labelled msg via the Progress path (so fn can revise the label
// through the [fx.Update]) and returns the unfinalized result. transient
// suppresses the non-TTY progress line, leaving only the caller's completion
// message in scrollback.
func spinResultProgress(
	ctx context.Context,
	msg string,
	transient bool,
	fn func(context.Context, *fx.Update) error,
	fields ...Field,
) *fx.WaitResult {
	b := clog.Spinner(msg).NonTTYSilent(transient).MessageStyle(cfg.messageStyle())
	for _, f := range fields {
		b = b.Str(f.Key, f.Val)
	}
	return b.Elapsed("elapsed",
		elapsed.WithGradientMax(cfg.gradientMax),
		elapsed.WithMinimum(cfg.elapsedMinimum),
	).Progress(ctx, fn)
}
