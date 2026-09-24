package scenario

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
)

func TestLookupCodeIsDeterministic(t *testing.T) {
	var out bytes.Buffer
	tool := lookupCodeTool(NewReporter(output.New(&out, &out, nil)))
	if !json.Valid(tool.Schema) {
		t.Fatalf("schema isn't JSON: %s", tool.Schema)
	}
	want := accessCode("alice")
	if len(want) != len("ZX-")+6 || !strings.HasPrefix(want, "ZX-") || strings.ToUpper(want) != want {
		t.Errorf("accessCode(alice) = %q", want)
	}
	for _, name := range []string{"alice", "Alice", " ALICE "} {
		got, err := tool.Handler(t.Context(), json.RawMessage(`{"name":`+`"`+name+`"}`))
		if err != nil || got != want {
			t.Errorf("lookup_code(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if accessCode("bob") == want {
		t.Error("bob and alice share a code")
	}
	if !strings.Contains(out.String(), `go handler: lookup_code("alice") → `+want) {
		t.Errorf("narration:\n%s", out.String())
	}
	if _, err := tool.Handler(t.Context(), json.RawMessage(`{"name":""}`)); err == nil {
		t.Error("an empty name got a code")
	}
}

func TestFingerprintMatchesTheExampleTool(t *testing.T) {
	// The value clutch/examples/tools/fingerprint/run.py prints for {"text":"clutch"}.
	if got := fingerprint("clutch"); got != "c278be9e2247" {
		t.Errorf("fingerprint(clutch) = %q", got)
	}
}
