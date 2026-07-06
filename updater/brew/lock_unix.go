//go:build unix

package brew

import (
	"os"
	"syscall"
)

// lockHeld reports whether another process currently holds the Homebrew lock
// file at path (the global `brew update` lock, or a per-formula upgrade lock).
// It mirrors Homebrew's own mechanism - a non-blocking flock(2) on the lock file
// - taking the lock only to release it at once: a successful acquire means no
// process holds it, while EWOULDBLOCK means one does. Any error opening the file
// is reported as "not held" so a probe we cannot perform never stalls the update
// indefinitely.
func lockHeld(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
