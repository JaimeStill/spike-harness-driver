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

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

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

func alive(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return !os.IsNotExist(err)
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}

const waitDelay = 5 * time.Second

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

func TestExitKillsLeftoverChildren(t *testing.T) {
	c := start(t, "echo", waitDelay, trackOrphan(t, true))
	checkExitSeen(t, c, "orphan", time.Second)
}

func TestExitWithEscapedChild(t *testing.T) {
	const delay = 300 * time.Millisecond
	c := start(t, "echo", delay, trackOrphan(t, false))
	checkExitSeen(t, c, "escape", delay+time.Second)
}

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
