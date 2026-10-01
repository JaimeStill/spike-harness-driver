package conform

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Status is how one capability fared on one cell.
type Status int

// The statuses a matrix cell takes. Skip applies to a whole cell: it never ran.
const (
	Pass Status = iota
	Fail
	NA
	Skip
)

// String is the word the matrix prints: FAIL is capitalized so it stands out among the rest.
func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Fail:
		return "FAIL"
	case NA:
		return "n/a"
	default:
		return "skip"
	}
}

// Result is what one capability found on one cell, with a one-line reason.
type Result struct {
	Capability string
	Status     Status
	Reason     string
}

// Cell is one harness over one provider, with what the suite needs to drive it. Every cell has
// its own Service, built from its own configuration, so a cell's defaults and provider setup
// are the ones a command run with that --harness and --provider has.
type Cell struct {
	Harness, Provider string
	// Profile is how the harness differs where a capability depends on it.
	Profile scenario.Profile
	// Pinned is the harness version the suite is validated against.
	Pinned string
	// Version returns the first line of the harness's --version output.
	Version func(context.Context) (string, error)
	// Needs are the preconditions of running the harness over the provider; one that fails
	// skips the cell.
	Needs []scenario.Need
	// Service opens this cell's sessions, with none of the user's --tools or --skills.
	Service *session.Service
	// VisionModel is the model a session sent an image runs on, and DefaultTakesImages whether
	// the default model takes images too, which leaves no text-only model to drop one.
	VisionModel        string
	DefaultTakesImages bool
	// Models returns the direct model client, which the audio capability's tool calls. An
	// error means no client can be set up, which leaves that capability not applicable.
	Models func() (scenario.Models, error)
}

// Label names the cell in the matrix and the reasons: harness/provider.
func (c Cell) Label() string { return c.Harness + "/" + c.Provider }

// Options are how a run goes.
type Options struct {
	// AnyVersion runs a harness whose version isn't the pinned one.
	AnyVersion bool
	// Timeout bounds each capability, which a cold router model can take minutes to answer.
	Timeout time.Duration
	// Progress, when set, is called as each capability starts. capability is empty for the
	// checks that come before them.
	Progress func(cell Cell, capability string)
	// Events, when set, receives every event of every exchange, tagged with the cell and
	// capability it belongs to. The default output leaves them out.
	Events func(cell Cell, capability string, ev harness.Event)
}

// Report is what one cell found.
type Report struct {
	Harness, Provider string
	// Version is the harness's version as the matrix shows it.
	Version string
	// Skipped is why the cell didn't run, or empty when it did.
	Skipped string
	// Results has one entry per capability, in Capabilities' order, or none for a skipped cell.
	Results []Result
	// Latency is what the exchange capability measured, or nil when it measured nothing.
	Latency *Latency
}

// Latency is how long a plain exchange took on a cell, as the exchange capability measures it:
// opening a session, which starts the harness, then the time from sending the prompt to the
// first text and to the exchange's end. One sample per run, so it shows the order of a
// harness's overhead, not a benchmark.
type Latency struct {
	Open, FirstText, Turn time.Duration
}

// Label names the cell as Cell.Label does.
func (r Report) Label() string { return r.Harness + "/" + r.Provider }

// Failures counts the capabilities that failed.
func (r Report) Failures() int {
	n := 0
	for _, res := range r.Results {
		if res.Status == Fail {
			n++
		}
	}
	return n
}

// Run runs each cell in turn, since the cells share the machine's one local model and the
// state directory, and returns a Report for each. A cell is skipped when a need fails or its
// harness isn't the pinned version; otherwise every capability runs, whatever the others find.
func Run(ctx context.Context, cells []Cell, opts Options) []Report {
	reports := make([]Report, 0, len(cells))
	for _, c := range cells {
		reports = append(reports, runCell(ctx, c, opts))
	}
	return reports
}

// runCell checks a cell's needs and version, then runs its capabilities.
func runCell(ctx context.Context, c Cell, opts Options) Report {
	rep := Report{Harness: c.Harness, Provider: c.Provider, Version: "-"}
	for _, n := range c.Needs {
		if err := n.Check(ctx); err != nil {
			rep.Skipped = fmt.Sprintf("need %s: %v", n.What, err)
			return rep
		}
	}
	if opts.Progress != nil {
		opts.Progress(c, "")
	}
	line, err := c.Version(ctx)
	switch {
	case err != nil && !opts.AnyVersion:
		rep.Skipped = fmt.Sprintf("the version of %s isn't known (%v); pass --any-version to run anyway", c.Harness, err)
		return rep
	case err != nil:
		rep.Version = "unknown"
	default:
		rep.Version = displayVersion(line)
		if rerr := CheckVersion(line, c.Pinned, opts.AnyVersion); rerr != nil {
			rep.Skipped = fmt.Sprintf("%s: %v", c.Harness, rerr)
			return rep
		}
		if !MatchesPin(line, c.Pinned) {
			rep.Version += " (pinned " + c.Pinned + ")"
		}
	}
	lat := &Latency{}
	for _, capability := range Capabilities {
		rep.Results = append(rep.Results, runCapability(ctx, c, capability, opts, lat))
	}
	if lat.Turn > 0 {
		rep.Latency = lat
	}
	return rep
}

// runCapability runs one capability under its own timeout. One that runs out of time fails,
// and the run goes on.
func runCapability(ctx context.Context, c Cell, capability Capability, opts Options, lat *Latency) Result {
	if opts.Progress != nil {
		opts.Progress(c, capability.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	e := &env{cell: c, capability: capability.Name, events: opts.Events, latency: lat}
	status, reason := capability.execute(ctx, e)
	if status == Fail && ctx.Err() != nil {
		reason += " (" + ctx.Err().Error() + ")"
	}
	return Result{Capability: capability.Name, Status: status, Reason: reason}
}

// CheckVersion fails when line, the first line of a harness's --version output, isn't the
// pinned version, unless anyVersion allows it. The message tells the user how to run anyway.
func CheckVersion(line, pinned string, anyVersion bool) error {
	if anyVersion || MatchesPin(line, pinned) {
		return nil
	}
	return fmt.Errorf("version %s, pinned %s; pass --any-version to run anyway", displayVersion(line), pinned)
}

// MatchesPin reports whether line holds the pinned version as a word of its own, so that
// "2.1.286 (Claude Code)" matches 2.1.286 and "0.99.21" doesn't match 0.99.2. A leading "v"
// is ignored.
func MatchesPin(line, pinned string) bool {
	return slices.ContainsFunc(strings.Fields(line), func(w string) bool {
		return strings.TrimPrefix(w, "v") == pinned
	})
}

// displayVersion is the word of line the matrix shows: its first, without a leading "v".
func displayVersion(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "unknown"
	}
	return strings.TrimPrefix(f[0], "v")
}
