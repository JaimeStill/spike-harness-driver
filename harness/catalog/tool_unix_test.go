//go:build unix

package catalog_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pidFile points the test tools at a file where they record the PID of the child they
// start, and returns a function that reports whether that child is still running.
func pidFile(t *testing.T) (alive func() bool) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "pid")
	t.Setenv("CATALOG_TEST_PIDFILE", file)
	return func() bool {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("the tool recorded no child: %v", err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		// Signal 0 checks for the process without touching it. A killed child that hasn't
		// been reaped yet is a zombie, which still answers, so give the reaper a moment.
		for range 50 {
			if syscall.Kill(pid, 0) != nil {
				return false
			}
			time.Sleep(20 * time.Millisecond)
		}
		return true
	}
}

// Cancelling a call kills the command's whole group, the script's child with it, well within
// the wait delay a leftover child would otherwise run into.
func TestCommandToolCancel(t *testing.T) {
	tool := commandTools(t)["slow"]
	alive := pidFile(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := tool.Handler(ctx, json.RawMessage(`{}`))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the call took %v after cancellation", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if alive() {
		t.Fatal("the script's child outlived the cancelled call")
	}
}

// A command that exits leaving a child holding its output answers at once, and the child is
// killed with the command's group.
func TestCommandToolLeavesNothingBehind(t *testing.T) {
	tool := commandTools(t)["leaves"]
	alive := pidFile(t)
	start := time.Now()
	out, err := tool.Handler(t.Context(), json.RawMessage(`{}`))
	if err != nil || out != "out\n" {
		t.Fatalf("Handler = %q, %v; want the command's output", out, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the call took %v, waiting on the leftover child", elapsed)
	}
	if alive() {
		t.Fatal("the command's child outlived the call")
	}
}
