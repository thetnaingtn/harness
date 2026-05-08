//go:build unix

package browser

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
)

// Why a watchdog subprocess?
//
// chromedp launches Chrome through os/exec; the resulting processes do
// not share a process group with us. On clean shutdown the alloc
// context cancel kills them. On SIGKILL / OOM / force-quit / panic that
// bypasses our cleanup chain, they reparent to PID 1 and survive
// indefinitely — each holding ~150 MB and a temp profile dir.
//
// startReaper spawns a single /bin/sh subprocess detached from our
// process group (Setsid). It accumulates user-data-dir paths from its
// stdin and, when the parent dies (stdin EOF), runs `pkill -9 -f` on
// each path and `rm -rf` the dirs. The watchdog cannot be reaped from
// outside because we never Wait on it; it exits on stdin EOF and the
// kernel cleans up.

var (
	reaperOnce sync.Once
	reaperMu   sync.Mutex
	reaperPipe io.WriteCloser
)

// reaperScript reads lines from stdin. A bare path is "tracked"; a line
// of the form "REMOVE <path>" is "untracked" (cleanly closed by us, so
// don't kill it on parent death). On EOF, kill -9 every still-tracked
// process whose argv contains the path, then rm -rf the path.
//
// Pure POSIX sh — no bash-isms. Tested on macOS /bin/sh (dash on Linux
// behaves the same).
const reaperScript = `
paths=""
while IFS= read -r line; do
    case "$line" in
        "REMOVE "*)
            removed="${line#REMOVE }"
            new=""
            for x in $paths; do
                [ "$x" = "$removed" ] && continue
                new="$new $x"
            done
            paths="$new"
            ;;
        *)
            paths="$paths $line"
            ;;
    esac
done
for p in $paths; do
    [ -z "$p" ] && continue
    pkill -9 -f "$p" 2>/dev/null
    rm -rf "$p" 2>/dev/null
done
`

func startReaper() {
	reaperOnce.Do(func() {
		cmd := exec.Command("/bin/sh", "-c", reaperScript)
		// Setsid detaches from our controlling terminal and process
		// group, so signals to us don't propagate to the watchdog.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		// Discard stdout/stderr so a closed terminal can't EPIPE the
		// watchdog before it runs cleanup.
		cmd.Stdout = nil
		cmd.Stderr = nil
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			return
		}
		// Deliberately do not Wait — the watchdog must outlive us.
		reaperPipe = stdin
	})
}

// trackDirForReaping registers a Chrome user-data-dir with the
// watchdog. If the parent dies hard before untrackDirForReaping is
// called, the watchdog will SIGKILL any process whose argv contains
// dir and rm -rf the dir.
func trackDirForReaping(dir string) {
	if dir == "" {
		return
	}
	startReaper()
	reaperMu.Lock()
	defer reaperMu.Unlock()
	if reaperPipe == nil {
		return
	}
	_, _ = fmt.Fprintf(reaperPipe, "%s\n", dir)
}

// untrackDirForReaping tells the watchdog this dir was torn down
// cleanly. Best-effort; failure is harmless (worst case the watchdog
// rm -rfs an already-empty dir on parent death).
func untrackDirForReaping(dir string) {
	if dir == "" {
		return
	}
	reaperMu.Lock()
	defer reaperMu.Unlock()
	if reaperPipe == nil {
		return
	}
	_, _ = fmt.Fprintf(reaperPipe, "REMOVE %s\n", dir)
}
