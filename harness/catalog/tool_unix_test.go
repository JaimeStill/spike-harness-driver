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
		for range 50 {
			if syscall.Kill(pid, 0) != nil {
				return false
			}
			time.Sleep(20 * time.Millisecond)
		}
		return true
	}
}

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
