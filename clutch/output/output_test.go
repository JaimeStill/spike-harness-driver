package output_test

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

func TestEventLine(t *testing.T) {
	x := uuid.MustParse("0192f3a4-5b6c-7d8e-9f00-112233445566")
	long := `{"type":"turn_start","padding":"` + strings.Repeat("x", 100) + `"}`
	tests := []struct {
		name string
		ev   harness.Event
		want string
	}{
		{
			name: "delta",
			ev:   harness.Event{SessionID: "session-0123456789", ExchangeID: x, Seq: 3, Kind: harness.EventTextDelta, Text: "Go"},
			want: `23456789 33445566    3 text_delta     "Go"`,
		},
		{
			name: "stop reason",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 9, Kind: harness.EventEnded, StopReason: "aborted"},
			want: `s 33445566    9 ended          "stop=aborted"`,
		},
		{
			name: "error",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 1, Kind: harness.EventError, Err: errors.New("boom")},
			want: `s 33445566    1 error          "boom"`,
		},
		{
			name: "tool without arguments",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 2, Kind: harness.EventToolCall, Tool: &harness.ToolEvent{Name: "bash"}},
			want: `s 33445566    2 tool_call      "bash"`,
		},
		{
			name: "tool call is compacted",
			ev: harness.Event{SessionID: "s", ExchangeID: x, Seq: 2, Kind: harness.EventToolCall,
				Tool: &harness.ToolEvent{Name: "lookup_code", Args: []byte(`{ "name": "alice" }`)}},
			want: `s 33445566    2 tool_call      ` + strconv.Quote(`lookup_code {"name":"alice"}`),
		},
		{
			name: "tool result is cut",
			ev: harness.Event{SessionID: "s", ExchangeID: x, Seq: 3, Kind: harness.EventToolResult,
				Tool: &harness.ToolEvent{Name: "read", Result: []byte(long)}},
			want: `s 33445566    3 tool_result    ` + strconv.Quote("read = "+long[:80]+"…"),
		},
		{
			name: "failed tool result",
			ev: harness.Event{SessionID: "s", ExchangeID: x, Seq: 3, Kind: harness.EventToolResult,
				Tool: &harness.ToolEvent{Name: "fingerprint", Result: []byte(`"exit status 1"`), IsError: true}},
			want: `s 33445566    3 tool_result    ` + strconv.Quote(`fingerprint failed: "exit status 1"`),
		},
		{
			name: "structured",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 5, Kind: harness.EventStructured, Structured: []byte(`{"answer": 42}`)},
			want: `s 33445566    5 structured     ` + strconv.Quote(`{"answer":42}`),
		},
		{
			name: "cut keeps whole characters",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 6, Kind: harness.EventHarness, Raw: []byte(strings.Repeat("x", 79) + "éé")},
			want: `s 33445566    6 harness        ` + strconv.Quote(strings.Repeat("x", 79)+"…"),
		},
		{
			name: "harness raw is cut",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 4, Kind: harness.EventHarness, Raw: []byte(long)},
			want: `s 33445566    4 harness        ` + strconv.Quote(long[:80]+"…"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := output.EventLine(tt.ev); got != tt.want {
				t.Errorf("EventLine =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestEventSkipsHarnessEventsUnlessAll(t *testing.T) {
	ev := harness.Event{Kind: harness.EventHarness, Raw: []byte(`{}`)}
	for _, all := range []bool{false, true} {
		var out bytes.Buffer
		o := output.New(&out, &bytes.Buffer{}, func() bool { return all })
		o.Event(ev)
		if got := out.Len() > 0; got != all {
			t.Errorf("all=%v: printed=%v", all, got)
		}
	}
}

func TestResultAndError(t *testing.T) {
	var out, errs bytes.Buffer
	o := output.New(&out, &errs, nil)
	o.Result(harness.Result{StopReason: "stop", Text: "hi", Usage: harness.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}}, nil)
	o.Result(harness.Result{StopReason: "stop", Structured: []byte(`{ "a": 1 }`)}, nil)
	o.Error(errors.New("bad"))
	want := "result: stop=stop usage=1/2 cache=3/4 err=<nil>\n  text: \"hi\"\n" +
		"result: stop=stop usage=0/0 cache=0/0 err=<nil>\n  text: \"\"\n  structured: {\"a\":1}\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if errs.String() != "error: bad\n" {
		t.Errorf("stderr = %q", errs.String())
	}
}

func TestRecord(t *testing.T) {
	x := uuid.MustParse("0192f3a4-5b6c-7d8e-9f00-112233445566")
	started := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		rec  harness.Record
		want string
	}{
		{
			rec:  harness.Record{ExchangeID: x, Started: started, Result: harness.Result{StopReason: "stop"}, Entries: []string{"a1", "b2", "c3"}, Request: harness.Request{Text: "hi"}},
			want: "exchange " + x.String() + "  2026-09-24T12:00:00Z  stop=stop  entries a1..c3 (3)\n  prompt: \"hi\"\n",
		},
		{
			rec:  harness.Record{ExchangeID: x, Started: started, Err: "boom", Request: harness.Request{Text: "hi"}},
			want: "exchange " + x.String() + "  2026-09-24T12:00:00Z  stop= err=boom  no entries\n  prompt: \"hi\"\n",
		},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		output.New(&out, &out, nil).Record(tt.rec)
		if out.String() != tt.want {
			t.Errorf("Record =\n%q\nwant\n%q", out.String(), tt.want)
		}
	}
}
