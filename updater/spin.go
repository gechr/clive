package updater

import (
	"context"

	"github.com/gechr/clog"
)

// Spin runs fn under a clog spinner labelled msg: on success it logs a
// completion line, and on failure it returns fn's error without the spinner
// logging its own error line, so the caller reports the failure exactly once.
func Spin(ctx context.Context, msg string, fn func(context.Context) error) error {
	res := clog.Spinner(msg).Elapsed("elapsed").Wait(ctx, fn)
	if err := res.Silent(); err != nil {
		return err
	}
	return res.Msg(msg)
}
