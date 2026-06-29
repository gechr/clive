package updater

import (
	"context"
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
