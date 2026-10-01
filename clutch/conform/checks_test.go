package conform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

func TestCheckShapes(t *testing.T) {
	tests := []struct {
		name    string
		got     []shapeColor
		wantErr string
	}{
		{"the fixture's", []shapeColor{{"circle", "red"}, {"square", "blue"}, {"triangle", "green"}}, ""},
		{"a rectangle for the square, in any case and order", []shapeColor{{"Triangle", " Green "}, {"RECTANGLE", "Blue"}, {"circle", "red"}}, ""},
		{"plurals and repeats", []shapeColor{{"circles", "red"}, {"squares", "blue"}, {"triangle", "green"}, {"triangle", "green"}}, ""},
		{"a swapped color", []shapeColor{{"circle", "blue"}, {"square", "red"}, {"triangle", "green"}}, "missing [circle red; square blue], extra [circle blue; square red]"},
		{"a missing shape", []shapeColor{{"circle", "red"}, {"square", "blue"}}, "missing [triangle green], extra []"},
		{"an extra shape", []shapeColor{{"circle", "red"}, {"square", "blue"}, {"triangle", "green"}, {"star", "yellow"}}, "extra [star yellow]"},
		{"none", nil, "missing [circle red; square blue; triangle green]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkShapes(tt.got)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("err = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSameMotto(t *testing.T) {
	_, motto, err := scenario.MottoSkill()
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{motto, "hold the line, shift the load", `"Hold the line — shift the load!"`, "  Hold   the line,\nshift the load. "} {
		if err := sameMotto(got, motto); err != nil {
			t.Errorf("%q: %v", got, err)
		}
	}
	for _, got := range []string{"", "Hold the line.", "Hold the line, shift the weight.", "The motto is: hold the line, shift the load."} {
		if err := sameMotto(got, motto); err == nil {
			t.Errorf("%q passed", got)
		}
	}
}

func TestSameValueComparesExactlyToCaseAndSpace(t *testing.T) {
	if err := sameValue("the city", "  PARIS ", "Paris"); err != nil {
		t.Error(err)
	}
	for _, got := range []string{"Paris, France", "The capital is Paris", "Pari", ""} {
		if err := sameValue("the city", got, "Paris"); err == nil {
			t.Errorf("%q passed", got)
		}
	}
}

func TestCheckCapitalAndColors(t *testing.T) {
	if err := checkCapital("Paris", "France"); err != nil {
		t.Error(err)
	}
	if err := checkCapital("Paris", "French Republic"); err == nil {
		t.Error("the wrong country passed")
	}
	if err := checkColors(3, []string{"Red", "yellow", " blue"}); err != nil {
		t.Error(err)
	}
	for name, c := range map[string]struct {
		count  int
		colors []string
	}{
		"a wrong count":  {2, []string{"red", "yellow", "blue"}},
		"a wrong color":  {3, []string{"red", "green", "blue"}},
		"a missing one":  {3, []string{"red", "blue"}},
		"a repeat":       {3, []string{"red", "red", "blue"}},
		"an extra color": {3, []string{"red", "yellow", "blue", "green"}},
	} {
		if err := checkColors(c.count, c.colors); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func TestDecode(t *testing.T) {
	var v struct {
		Code string `json:"code"`
	}
	if err := decode(json.RawMessage(`{"code":"ZX-1"}`), &v); err != nil || v.Code != "ZX-1" {
		t.Errorf("v = %+v, err = %v", v, err)
	}
	if err := decode(nil, &v); err == nil || !strings.Contains(err.Error(), "without a structured response") {
		t.Errorf("no response: err = %v", err)
	}
	if err := decode(json.RawMessage(`{"code":`), &v); err == nil {
		t.Error("malformed JSON decoded")
	}
}

func TestToolRan(t *testing.T) {
	call := harness.Event{Kind: harness.EventToolCall, Tool: &harness.ToolEvent{Name: "lookup_code"}}
	result := harness.Event{Kind: harness.EventToolResult, Tool: &harness.ToolEvent{Name: "lookup_code"}}
	other := harness.Event{Kind: harness.EventToolCall, Tool: &harness.ToolEvent{Name: "read"}}
	if err := toolRan([]harness.Event{other, call, result}, "lookup_code"); err != nil {
		t.Error(err)
	}
	if err := toolRan([]harness.Event{other}, "lookup_code"); err == nil || !strings.Contains(err.Error(), "never called") {
		t.Errorf("no call: err = %v", err)
	}
	if err := toolRan([]harness.Event{call}, "lookup_code"); err == nil || !strings.Contains(err.Error(), "no result") {
		t.Errorf("no result: err = %v", err)
	}
}

func TestNewCodeWordsAndCodesVary(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		seen[newCodeWord()] = true
		seen[newCode()] = true
	}
	if len(seen) < 30 {
		t.Errorf("only %d distinct values in 40 draws", len(seen))
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("one\n  two", 60); got != "one two" {
		t.Errorf("got %q", got)
	}
	if got := truncate("héllo wörld", 2); got != "h…" {
		t.Errorf("got %q", got)
	}
}
