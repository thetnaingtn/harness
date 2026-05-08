//go:build !unix

package browser

// Stubs for platforms without POSIX pkill/sh. Windows callers fall back
// to chromedp's own teardown; hard-kill orphans are a known limitation
// there until we add a native implementation (e.g. Job Objects).

func trackDirForReaping(dir string)   {}
func untrackDirForReaping(dir string) {}
