//go:build unix

package brew

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/require"
)

// TestWaitLockBlocksUntilReleased proves the core guarantee behind the fetch
// pause: waitLock does not return - and so the update does not advance to `brew
// upgrade` - while another process holds the lock. It holds the lock via an
// independent descriptor, confirms the wait is still blocked a poll interval
// later, then releases and confirms it returns.
func TestWaitLockBlocksUntilReleased(t *testing.T) {
	t.Parallel()

	lockPath := heldLock(t)

	r := &runner{}
	done := make(chan error, 1)
	go func() { done <- r.waitLock(context.Background(), lockPath.path) }()

	// Still blocked while the lock is held.
	select {
	case <-done:
		t.Fatal("waitLock returned while the lock was still held")
	case <-time.After(brewLockPoll + 500*time.Millisecond):
	}

	// Release; the wait must now unblock promptly.
	lockPath.release(t)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("waitLock did not return after the lock was released")
	}
}

// TestWaitLockRespectsContext confirms the wait is bounded: a cancelled context
// (the fetchTimeout firing) ends the wait rather than blocking forever on a lock
// that never frees.
func TestWaitLockRespectsContext(t *testing.T) {
	t.Parallel()

	lockPath := heldLock(t)
	t.Cleanup(func() { lockPath.release(t) })

	r := &runner{}
	ctx, cancel := context.WithTimeout(context.Background(), brewLockPoll+500*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, r.waitLock(ctx, lockPath.path), context.DeadlineExceeded)
}

// TestRunAwaitingLockRelabelsWhileBlocked proves the spinner reads "Waiting"
// only while genuinely blocked: runAwaitingLock reports onWait(true) when it
// starts waiting on a held formula lock, does not run the command meanwhile, and
// reports onWait(false) - then runs - once the lock frees.
func TestRunAwaitingLockRelabelsWhileBlocked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prefix := filepath.Join(dir, "prefix")
	locks := filepath.Join(prefix, "var", "homebrew", "locks")
	require.NoError(t, os.MkdirAll(locks, 0o755))

	// Hold the formula lock, as a concurrent `brew upgrade app` would.
	holder, err := os.OpenFile(
		filepath.Join(locks, "app.formula.lock"),
		os.O_RDWR|os.O_CREATE,
		0o644,
	)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))

	brew := filepath.Join(dir, "brew")
	require.NoError(
		t,
		os.WriteFile(brew, []byte("#!/bin/sh\n[ \"$1\" = upgrade ] && exit 0\n"), 0o755),
	)

	// prefix is pre-seeded so the lock pre-check is a pure file probe with no
	// `brew --prefix` subprocess in the timing-sensitive path below.
	r := &runner{brew: brew, prefix: prefix, cfg: New(clive.Info{}, WithFormula("app"))}

	var mu sync.Mutex
	var events []bool
	onWait := func(waiting bool) { mu.Lock(); defer mu.Unlock(); events = append(events, waiting) }
	snapshot := func() []bool { mu.Lock(); defer mu.Unlock(); return append([]bool(nil), events...) }

	done := make(chan error, 1)
	go func() { done <- r.runAwaitingLock(context.Background(), onWait, "upgrade", "app") }()

	// Blocked while the lock is held: it has entered the wait but not returned.
	select {
	case <-done:
		t.Fatal("runAwaitingLock returned while the formula lock was held")
	case <-time.After(brewLockPoll + 500*time.Millisecond):
	}
	require.Equal(t, []bool{true}, snapshot())

	// Release; it relabels back to active and completes.
	require.NoError(t, syscall.Flock(int(holder.Fd()), syscall.LOCK_UN))
	require.NoError(t, holder.Close())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("runAwaitingLock did not return after the lock was released")
	}
	require.Equal(t, []bool{true, false}, snapshot())
}

// heldLock is a lock file flocked by an independent descriptor, exactly as a
// concurrent brew process would hold it.
type heldLockFile struct {
	path string
	f    *os.File
}

func heldLock(t *testing.T) *heldLockFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	return &heldLockFile{path: path, f: f}
}

func (h *heldLockFile) release(t *testing.T) {
	t.Helper()
	if h.f == nil {
		return
	}
	require.NoError(t, syscall.Flock(int(h.f.Fd()), syscall.LOCK_UN))
	require.NoError(t, h.f.Close())
	h.f = nil
}
