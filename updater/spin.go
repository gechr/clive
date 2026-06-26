package updater

import (
	"context"
	"sync"
	"time"

	"github.com/gechr/clog"
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
	elapsedOnce.Do(func() {
		f := clog.Default.FieldFormats()
		f.ElapsedMinimum = elapsedMinimum
		clog.Default.SetFieldFormats(f)
	})

	b := clog.Spinner(msg)
	for _, f := range fields {
		b = b.Str(f.Key, f.Val)
	}
	res := b.Elapsed("elapsed").Wait(ctx, fn)
	if err := res.Silent(); err != nil {
		return err
	}
	return res.Symbol(doneSymbol).Msg(msg)
}
