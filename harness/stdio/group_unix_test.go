//go:build unix

package stdio_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// trackOrphan has the peer record the PID of the child it orphans, and checks when the test
// ends that the child didn't outlive the harness. It kills a child that did, so no test leaves
// a process behind.
func trackOrphan(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "orphan.pid")
	t.Cleanup(func() {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("the peer recorded no orphan: %v", err)
			return
		}
		pid, _ := strconv.Atoi(string(data))
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

// TestCloseKillsLeftoverChildren covers a harness that exits on Close but leaves a child
// holding its stdout: the child is killed with the harness's process group.
func TestCloseKillsLeftoverChildren(t *testing.T) {
	c := start(t, "orphaner", 200*time.Millisecond, trackOrphan(t))
	begin := time.Now()
	_ = c.Close()
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("Close took %s", d)
	}
}

// TestExitKillsLeftoverChildren covers a harness that exits on its own while a child still
// holds its stdout: the exit is still seen, and the child is killed with the harness's process
// group.
func TestExitKillsLeftoverChildren(t *testing.T) {
	c := start(t, "echo", 200*time.Millisecond, trackOrphan(t))
	begin := time.Now()
	if _, err := c.Call(t.Context(), testCmd{Op: "orphan"}); err == nil {
		t.Fatal("Call to a peer that exited succeeded")
	}
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("the exit took %s to be seen", d)
	}
	ev, _ := nextEvent(t, c)
	if ev.Kind != harness.EventError {
		t.Fatalf("event = %+v, want EventError", ev)
	}
	if _, ok := nextEvent(t, c); ok {
		t.Fatal("Events did not close after the exit")
	}
}
