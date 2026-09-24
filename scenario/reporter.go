package scenario

import (
	"fmt"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/output"
)

// Reporter narrates a scenario through the Output every clutch command renders with, so a
// scenario's events and results read the same as a direct command's.
type Reporter struct {
	out *output.Output
}

// NewReporter returns a Reporter writing through out.
func NewReporter(out *output.Output) *Reporter {
	return &Reporter{out: out}
}

// Intent prints step i of n's intent as a heading, set off by a blank line.
func (r *Reporter) Intent(i, n int, intent string) {
	r.out.Printf("")
	r.out.Printf("[%d/%d] %s", i, n, intent)
}

// Note prints one indented line of prose.
func (r *Reporter) Note(format string, args ...any) {
	r.out.Printf("  "+format, args...)
}

// Event prints one event line.
func (r *Reporter) Event(ev harness.Event) { r.out.Event(ev) }

// Result prints an exchange's result.
func (r *Reporter) Result(res harness.Result, err error) { r.out.Result(res, err) }

// Cancelling notes that an exchange is being cancelled after n text deltas.
func (r *Reporter) Cancelling(n int) {
	r.Note("%s", fmt.Sprintf("-- cancelling after %d text deltas", n))
}
