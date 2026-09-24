package output_test

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/output"
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
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 1, Kind: harness.EventError, Err: "boom"},
			want: `s 33445566    1 error          "boom"`,
		},
		{
			name: "tool",
			ev:   harness.Event{SessionID: "s", ExchangeID: x, Seq: 2, Kind: harness.EventToolCall, Tool: &harness.ToolEvent{Name: "bash"}},
			want: `s 33445566    2 tool_call      "bash"`,
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
	o.Result(harness.Result{StopReason: "stop", Text: "hi", Usage: harness.Usage{Input: 1, Output: 2}}, nil)
	o.Error(errors.New("bad"))
	want := "result: stop=stop usage=1/2 err=<nil>\n  text: \"hi\"\n"
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
