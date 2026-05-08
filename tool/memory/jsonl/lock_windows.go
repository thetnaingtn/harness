//go:build windows

package jsonl

import "os"

// lockExclusive is a no-op on Windows. Cross-process safety on Windows
// would require LockFileEx / Mutex; out of scope for this default
// implementation. Single-process Windows users are unaffected.
func lockExclusive(_ *os.File) error { return nil }

// lockShared is a no-op on Windows. See lockExclusive.
func lockShared(_ *os.File) error { return nil }
