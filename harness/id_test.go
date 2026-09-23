package harness_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

var uuidv7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIDFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := harness.NewID()
		if !uuidv7.MatchString(id) {
			t.Fatalf("NewID() = %q, not a UUIDv7", id)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = true
	}
}

func TestNewIDSortsByTime(t *testing.T) {
	a := harness.NewID()
	time.Sleep(2 * time.Millisecond)
	b := harness.NewID()
	if a >= b {
		t.Fatalf("later ID %q does not sort after %q", b, a)
	}
}
