package output

import (
	"fmt"
	"io"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// harnessDetail is how much of a harness event's raw record an event line keeps.
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

// Result writes an exchange's result and the error Wait returned with it.
func (o *Output) Result(res harness.Result, err error) {
	o.Printf("result: stop=%s usage=%d/%d err=%v", res.StopReason, res.Usage.Input, res.Usage.Output, err)
	o.Printf("  text: %q", res.Text)
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

// EventLine formats ev as Event prints it.
func EventLine(ev harness.Event) string {
	detail := ev.Text
	switch {
	case ev.Kind == harness.EventHarness:
		detail = string(ev.Raw)
		if len(detail) > harnessDetail {
			detail = detail[:harnessDetail] + "…"
		}
	case ev.Kind == harness.EventMessageEnd || ev.Kind == harness.EventEnded || ev.Kind == harness.EventCancelled:
		detail = "stop=" + ev.StopReason
	case ev.Err != "":
		detail = ev.Err
	case ev.Tool != nil:
		detail = ev.Tool.Name
	}
	return fmt.Sprintf("%s %s %4d %-14s %q", Short(ev.SessionID), Short(ev.ExchangeID.String()), ev.Seq, ev.Kind, detail)
}

// Short keeps an ID's last eight characters, which differ between IDs made in the same
// millisecond.
func Short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}
