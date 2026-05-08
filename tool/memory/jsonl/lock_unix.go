//go:build !windows

package jsonl

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockExclusive acquires an exclusive (write) advisory lock on f.
// Blocks until the lock is available. Released when the file descriptor
// is closed (caller's defer f.Close() does this).
func lockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

// lockShared acquires a shared (read) advisory lock on f. Multiple
// shared lockers can coexist; an exclusive locker blocks them.
func lockShared(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_SH)
}
