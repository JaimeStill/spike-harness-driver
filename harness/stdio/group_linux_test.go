//go:build linux

package stdio_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// The orphan tests run only on Linux, where alive can tell a zombie from a live process
// through /proc.

// detach starts cmd in a session of its own, outside the peer's process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// trackOrphan has the peer record the PID of the child it orphans. When the test ends, it
// checks that the child is gone if wantGone, and kills it either way, so no test leaves a
// process behind.
func trackOrphan(t *testing.T, wantGone bool) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "orphan.pid")
	t.Cleanup(func() {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("the peer recorded no orphan: %v", err)
			return
		}
		pid, _ := strconv.Atoi(string(data))
		if !wantGone {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		deadline := time.Now().Add(2 * time.Second)
		for alive(pid) {
			if time.Now().After(deadline) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Errorf("orphan %d outlived the harness", pid)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return orphanPIDEnv + "=" + file
}

// alive reports whether pid is a running process. A killed process that its new parent hasn't
// reaped yet is a zombie, which counts as gone.
func alive(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return !os.IsNotExist(err)
	}
	// The state follows the parenthesized command name.
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}

// waitDelay is long, so a test that finishes well within it shows the wait delay wasn't
// needed.
const waitDelay = 5 * time.Second

// TestCloseKillsLeftoverChildren covers a harness that exits on Close but leaves a child
// holding its stdout. Close returns as soon as the harness exits, without error, and the child
// is killed with the harness's process group.
func TestCloseKillsLeftoverChildren(t *testing.T) {
	c := start(t, "orphaner", waitDelay, trackOrphan(t, true))
	begin := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Close took %s", d)
	}
	if ev, ok := nextEvent(t, c); ok {
		t.Fatalf("a clean Close reported %+v", ev)
	}
}

// TestExitKillsLeftoverChildren covers a harness that exits on its own while a child still
// holds its stdout. The exit is seen at once, and the child is killed with the harness's
// process group.
func TestExitKillsLeftoverChildren(t *testing.T) {
	c := start(t, "echo", waitDelay, trackOrphan(t, true))
	checkExitSeen(t, c, "orphan", time.Second)
}

// TestExitWithEscapedChild covers a child that left the harness's process group, so the group
// kill can't reach it. The pipes it holds are closed after the wait delay, and the exit is
// seen then.
func TestExitWithEscapedChild(t *testing.T) {
	const delay = 300 * time.Millisecond
	c := start(t, "echo", delay, trackOrphan(t, false))
	checkExitSeen(t, c, "escape", delay+time.Second)
}

// checkExitSeen sends op, which makes the peer exit, and checks that the exit fails the call
// within the given time, then closes Events, with Err reporting it.
func checkExitSeen(t *testing.T, c *stdio.Client, op string, within time.Duration) {
	t.Helper()
	begin := time.Now()
	if _, err := c.Call(t.Context(), testCmd{Op: op}); err == nil {
		t.Fatal("Call to a peer that exited succeeded")
	}
	if d := time.Since(begin); d > within {
		t.Fatalf("the exit took %s to be seen", d)
	}
	if ev, ok := nextEvent(t, c); ok {
		t.Fatalf("Events delivered %+v instead of closing after the exit", ev)
	}
	if c.Err() == nil {
		t.Fatal("Err = nil after an exit Close didn't ask for")
	}
}
