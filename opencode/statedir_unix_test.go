//go:build unix

package opencode

import (
	"os"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

func TestAStateDirectoryOthersCanWriteIsRefused(t *testing.T) {
	d := fakeDriver(t.TempDir())
	if err := os.Mkdir(d.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d.StateDir, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := d.Open(t.Context(), harness.Options{Provider: fakeProvider})
	if err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatalf("Open = %v, want the state directory refused", err)
	}
}

func TestAStateDirectoryIsCreatedPrivate(t *testing.T) {
	d := fakeDriver(t.TempDir())
	open(t, d, harness.Options{})
	if fi, err := os.Stat(d.StateDir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state directory = %v, %v; want it private", fi, err)
	}
}
