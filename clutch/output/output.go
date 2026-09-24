package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// harnessDetail is how much of a harness event's raw record, a tool's arguments or result, or a
// structured response an event line keeps.
const harnessDetail = 80

// Output writes results to one stream and errors to another.
type Output struct {
	out, err io.Writer
	// all reports whether events with no normalized meaning are printed. It is read at each
	// event, so it can follow a flag parsed after the Output is built.
	all func() bool
}

// New returns an Output over stdout and stderr. A nil all prints only normalized events.
func New(stdout, stderr io.Writer, all func() bool) *Output {
	if all == nil {
		all = func() bool { return false }
	}
	return &Output{out: stdout, err: stderr, all: all}
}

// Printf writes one line of prose to stdout.
func (o *Output) Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(o.out, format+"\n", args...)
}

// Event writes ev as one line. An EventHarness is skipped unless all reports true.
func (o *Output) Event(ev harness.Event) {
	if ev.Kind == harness.EventHarness && !o.all() {
		return
	}
	_, _ = fmt.Fprintln(o.out, EventLine(ev))
}

// Result writes an exchange's result and the error Wait returned with it. Usage is input/output,
// then cache read/write, since with a warm cache the input alone understates the prompt. The
// structured response, when the request asked for one, is printed whole: it is the answer.
func (o *Output) Result(res harness.Result, err error) {
	u := res.Usage
	o.Printf("result: stop=%s usage=%d/%d cache=%d/%d err=%v", res.StopReason, u.Input, u.Output, u.CacheRead, u.CacheWrite, err)
	o.Printf("  text: %q", res.Text)
	if len(res.Structured) > 0 {
		o.Printf("  structured: %s", compact(res.Structured))
	}
}

// Record writes one recorded exchange: its ID, the span of harness entries it appended, how
// it ended, and its prompt.
func (o *Output) Record(r harness.Record) {
	span := "no entries"
	switch n := len(r.Entries); n {
	case 0:
	case 1:
		span = "entry " + r.Entries[0]
	default:
		span = fmt.Sprintf("entries %s..%s (%d)", r.Entries[0], r.Entries[n-1], n)
	}
	ended := "stop=" + r.Result.StopReason
	if r.Err != "" {
		ended += " err=" + r.Err
	}
	o.Printf("exchange %s  %s  %s  %s", r.ExchangeID, r.Started.Format(time.RFC3339), ended, span)
	o.Printf("  prompt: %q", r.Request.Text)
}

// Error writes the error a command ended with to stderr.
func (o *Output) Error(err error) {
	_, _ = fmt.Fprintf(o.err, "error: %v\n", err)
}

// EventLine formats ev as Event prints it. A tool event shows the tool's name and a cut view of
// its arguments, for a call, or its result, for a result, marked when the tool failed.
func EventLine(ev harness.Event) string {
	detail := ev.Text
	switch {
	case ev.Kind == harness.EventHarness:
		detail = cut(string(ev.Raw), harnessDetail)
	case ev.Kind == harness.EventMessageEnd || ev.Kind == harness.EventEnded || ev.Kind == harness.EventCancelled:
		detail = "stop=" + ev.StopReason
	case ev.Kind == harness.EventStructured:
		detail = cut(compact(ev.Structured), harnessDetail)
	case ev.Err != nil:
		detail = ev.Err.Error()
	case ev.Tool != nil:
		detail = toolDetail(ev.Kind, ev.Tool)
	}
	return fmt.Sprintf("%s %s %4d %-14s %q", Short(ev.SessionID), Short(ev.ExchangeID.String()), ev.Seq, ev.Kind, detail)
}

// toolDetail is a tool event's detail: the name, then the arguments of a call or the result of
// a result, each compacted and cut.
func toolDetail(kind harness.EventKind, t *harness.ToolEvent) string {
	payload, sep := t.Args, " "
	if kind == harness.EventToolResult {
		payload, sep = t.Result, " = "
		if t.IsError {
			sep = " failed: "
		}
	}
	if len(payload) == 0 {
		return t.Name
	}
	return t.Name + sep + cut(compact(payload), harnessDetail)
}

// compact returns raw JSON without insignificant space, or as it is if it doesn't parse.
func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// cut keeps at most n bytes of s, ending on a whole character, and marks what it dropped.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// Short keeps an ID's last eight characters, which differ between IDs made in the same
// millisecond.
func Short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}
