package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// replay decodes every line of a testdata transcript with one codec, as the Client's reader
// would, and returns the events.
func replay(t *testing.T, name string) []harness.Event {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c := newCodec()
	var events []harness.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		fr, err := c.Decode(slices.Clone(sc.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if fr.Response != nil || fr.Request != nil {
			t.Fatalf("transcript line decoded as a response or request: %s", sc.Bytes())
		}
		events = append(events, fr.Events...)
	}
	return events
}

// normalized drops EventHarness and merges each run of deltas of one kind, for comparing a
// transcript's shape.
func normalized(events []harness.Event) []harness.EventKind {
	var kinds []harness.EventKind
	for _, ev := range events {
		if ev.Kind == harness.EventHarness {
			continue
		}
		if n := len(kinds); n > 0 && kinds[n-1] == ev.Kind && (ev.Kind == harness.EventTextDelta || ev.Kind == harness.EventThinkingDelta) {
			continue
		}
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func find(events []harness.Event, kind harness.EventKind) []harness.Event {
	var out []harness.Event
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestATurnWithToolsAndARespondCall(t *testing.T) {
	events := replay(t, "tools.jsonl")
	want := []harness.EventKind{
		harness.EventStarted, harness.EventToolCall, harness.EventToolResult, harness.EventStructured,
		harness.EventLimit, harness.EventTextDelta, harness.EventMessageEnd, harness.EventEnded,
	}
	if got := normalized(events); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	call, result := find(events, harness.EventToolCall)[0], find(events, harness.EventToolResult)[0]
	if call.Tool.Name != "echo" || string(call.Tool.Args) != `{"text":"seven"}` {
		t.Errorf("call = %+v, want echo without the server's prefix", call.Tool)
	}
	if result.Tool.Name != "echo" || result.Tool.CallID != call.Tool.CallID || result.Tool.IsError {
		t.Errorf("result = %+v, want echo's, matched to its call", result.Tool)
	}
	if s := find(events, harness.EventStructured)[0]; string(s.Structured) != `{"city":"Paris"}` {
		t.Errorf("structured = %s", s.Structured)
	}
	end := find(events, harness.EventMessageEnd)[0]
	want4 := harness.Usage{Input: 18, Output: 285, CacheRead: 4420, CacheWrite: 4749}
	if end.StopReason != "end_turn" || end.Usage == nil || *end.Usage != want4 || end.Text == "" {
		t.Errorf("message end = stop %q usage %+v text %q; want the turn's summed usage", end.StopReason, end.Usage, end.Text)
	}
	limit := find(events, harness.EventLimit)[0].Limit
	if limit.Status != "allowed" || limit.Kind != "five_hour" || limit.Utilization != 0.03 || limit.ResetsAt.Unix() != 1790871000 {
		t.Errorf("limit = %+v", limit)
	}
}

func TestASkillTurn(t *testing.T) {
	events := replay(t, "skill.jsonl")
	calls := find(events, harness.EventToolCall)
	if len(calls) != 1 || calls[0].Tool.Name != "Skill" {
		t.Fatalf("calls = %+v, want the Skill tool as it is named", calls)
	}
	if got := normalized(events); got[len(got)-1] != harness.EventEnded || got[len(got)-2] != harness.EventMessageEnd {
		t.Fatalf("kinds = %v", got)
	}
}

func TestAnInterruptedTurnIsCancelled(t *testing.T) {
	events := replay(t, "aborted.jsonl")
	got := normalized(events)
	tail := got[len(got)-3:]
	if !slices.Equal(tail, []harness.EventKind{harness.EventMessageEnd, harness.EventCancelled, harness.EventEnded}) {
		t.Fatalf("kinds = %v, want the end cancelled", got)
	}
	if end := find(events, harness.EventMessageEnd)[0]; end.StopReason != "aborted" {
		t.Fatalf("stop = %q, want aborted", end.StopReason)
	}
	if errs := find(events, harness.EventError); len(errs) != 0 {
		t.Fatalf("an interrupt is no error: %+v", errs)
	}
}

func TestATurnTheLimitRefusedIsALimitError(t *testing.T) {
	events := replay(t, "limited.jsonl")
	errs := find(events, harness.EventError)
	if len(errs) != 1 {
		t.Fatalf("errors = %+v", errs)
	}
	var limit *harness.LimitError
	if !errors.As(errs[0].Err, &limit) || !errors.Is(errs[0].Err, harness.ErrUsageLimit) {
		t.Fatalf("error = %v, want a *harness.LimitError", errs[0].Err)
	}
	if !limit.ResetsAt.Equal(time.Unix(1790871000, 0)) {
		t.Errorf("resets at %s", limit.ResetsAt)
	}
	if end := find(events, harness.EventMessageEnd)[0]; end.StopReason != "error" {
		t.Errorf("stop = %q, want error", end.StopReason)
	}
}

func TestTurnErrors(t *testing.T) {
	status := func(n int) *int { return &n }
	tests := []struct {
		name    string
		limit   *harness.LimitStatus
		r       result
		limited bool
		resets  int64
	}{
		{name: "a failure", r: result{IsError: true, Errors: []string{"boom"}}},
		{name: "an HTTP 429", r: result{IsError: true, Result: "API Error: 429", APIErrorStatus: status(429)}, limited: true},
		{name: "a rejected notice", limit: &harness.LimitStatus{Status: "rejected", ResetsAt: time.Unix(1790000000, 0)},
			r: result{IsError: true, Result: "denied"}, limited: true, resets: 1790000000},
		{name: "an allowed notice", limit: &harness.LimitStatus{Status: "allowed"}, r: result{IsError: true, Result: "denied"}},
		{name: "the limit message", r: result{IsError: true, Result: "Claude AI usage limit reached|1790871000"}, limited: true, resets: 1790871000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCodec()
			c.limit = tt.limit
			err := c.turnError(tt.r)
			var limit *harness.LimitError
			if errors.As(err, &limit) != tt.limited {
				t.Fatalf("error = %v, limited %v", err, tt.limited)
			}
			if tt.resets != 0 && limit.ResetsAt.Unix() != tt.resets {
				t.Errorf("resets at %s, want %d", limit.ResetsAt, tt.resets)
			}
		})
	}
}

// A rejected notice explains only its own turn's failure: the next turn's init forgets it.
func TestALimitNoticeLastsOneTurn(t *testing.T) {
	c := newCodec()
	var events []harness.Event
	for _, l := range []string{
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1790871000,"rateLimitType":"five_hour"}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}`,
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["boom"]}`,
	} {
		fr, err := c.Decode([]byte(l))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, fr.Events...)
	}
	errs := find(events, harness.EventError)
	if len(errs) != 1 {
		t.Fatalf("errors = %+v", errs)
	}
	if errors.Is(errs[0].Err, harness.ErrUsageLimit) || errs[0].Err.Error() != "claude: boom" {
		t.Fatalf("error = %v, want the turn's own failure, not the earlier turn's limit", errs[0].Err)
	}
}

func TestARejectedRespondCall(t *testing.T) {
	c := newCodec()
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__driver__respond_0f3a9c21","input":{"town":"Paris"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"missing properties: [\"city\"]"}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"mcp__driver__respond_0f3a9c21","input":{"city":"Paris"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"Accepted."}]}}`,
		// A result that isn't flagged an error but isn't the bridge's acceptance either is no
		// structured response.
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"mcp__driver__respond_0f3a9c21","input":{"city":"Lyon"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"something else"}]}}`,
	}
	var events []harness.Event
	for _, l := range lines {
		fr, err := c.Decode([]byte(l))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, fr.Events...)
	}
	got := normalized(events)
	want := []harness.EventKind{harness.EventStructuredRejected, harness.EventStructured, harness.EventStructuredRejected}
	if !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want a rejection, the response, then a rejection, and no tool events", got)
	}
	if events[1].Err == nil || events[1].Err.Error() != `claude: respond: missing properties: ["city"]` {
		t.Errorf("rejection = %v", events[1].Err)
	}
	if events[5].Err == nil || events[5].Err.Error() != `claude: respond: something else` {
		t.Errorf("second rejection = %v", events[5].Err)
	}
}

func TestControlMessages(t *testing.T) {
	c := newCodec()
	line, err := c.Encode("7", controlRequest{Subtype: "interrupt"})
	if err != nil || string(line) != `{"type":"control_request","request_id":"7","request":{"subtype":"interrupt","hooks":null}}` {
		t.Fatalf("Encode = %s, %v", line, err)
	}

	fr, err := c.Decode([]byte(`{"type":"control_response","response":{"subtype":"error","request_id":"7","error":"no turn"}}`))
	if err != nil || fr.Response == nil || fr.Response.ID != "7" || fr.Response.Err == nil {
		t.Fatalf("Decode error response = %+v, %v", fr.Response, err)
	}

	fr, err = c.Decode([]byte(`{"type":"control_request","request_id":"abc","request":{"subtype":"mcp_message","server_name":"driver","message":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}}`))
	if err != nil || fr.Request == nil || fr.Request.ID != "abc" {
		t.Fatalf("Decode request = %+v, %v", fr.Request, err)
	}
	if r := fr.Request.Body.(controlRequest); r.Subtype != "mcp_message" || r.ServerName != "driver" {
		t.Fatalf("request body = %+v", r)
	}

	reply, err := c.Reply(stdio.Request{ID: "abc"}, map[string]json.RawMessage{"mcp_response": json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)})
	want := `{"type":"control_response","response":{"subtype":"success","request_id":"abc","response":{"mcp_response":{"jsonrpc":"2.0","id":1,"result":{}}}}}`
	if err != nil || string(reply) != want {
		t.Fatalf("Reply = %s, %v", reply, err)
	}
	reply, _ = c.Reply(stdio.Request{ID: "abc"}, errors.New("nope"))
	if string(reply) != `{"type":"control_response","response":{"subtype":"error","request_id":"abc","error":"nope"}}` {
		t.Fatalf("error Reply = %s", reply)
	}
}
