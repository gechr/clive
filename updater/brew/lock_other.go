//go:build !unix

package brew

// lockHeld is a no-op on platforms without flock(2): Homebrew does not run
// there, so there is never a lock to wait on.
func lockHeld(string) bool { return false }
