//go:build unix

package opencode

import (
	"os"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// OpenCode reads configuration from its state directory that can name commands to run, so a
// directory others can write to is refused before OpenCode starts.
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

// A state directory that doesn't exist yet is created private to the user.
func TestAStateDirectoryIsCreatedPrivate(t *testing.T) {
	d := fakeDriver(t.TempDir())
	open(t, d, harness.Options{})
	if fi, err := os.Stat(d.StateDir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state directory = %v, %v; want it private", fi, err)
	}
}
