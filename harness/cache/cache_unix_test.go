//go:build unix

package cache_test

import (
	"os"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness/cache"
)

func TestARootOthersCanWriteIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := cache.Dir(root, "x", "0", func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatalf("Dir = %v, want the root refused", err)
	}
}

func TestPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cache.Private(dir); err != nil {
		t.Fatalf("Private on a 0700 directory = %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := cache.Private(dir); err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatalf("Private on a 0777 directory = %v, want it refused", err)
	}
}
