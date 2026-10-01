//go:build unix

package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Pi runs commands its agent directory's files name, so a directory others can write to is
// refused before Pi starts.
func TestAnAgentDirectoryOthersCanWriteIsRefused(t *testing.T) {
	d := fakeDriver("ok")
	d.AgentDir = filepath.Join(t.TempDir(), "agent")
	if err := os.Mkdir(d.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d.AgentDir, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := d.Open(t.Context(), harness.Options{Provider: "llama.cpp", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatalf("Open = %v, want the agent directory refused", err)
	}
}
