package conform

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
)

func TestVersionGate(t *testing.T) {
	tests := []struct {
		name, line, pinned string
		anyVersion         bool
		wantErr            string
	}{
		{"pinned", "0.99.2", "0.99.2", false, ""},
		{"pinned with a suffix", "2.1.286 (Claude Code)", "2.1.286", false, ""},
		{"pinned with a v", "v1.18.34", "1.18.34", false, ""},
		{"older", "0.99.1", "0.99.2", false, "version 0.99.1, pinned 0.99.2; pass --any-version to run anyway"},
		{"a longer version isn't the pin", "0.99.21", "0.99.2", false, "version 0.99.21, pinned 0.99.2"},
		{"the pin in a word that isn't the version", "pi (0.99.2.beta)", "0.99.2", false, "pinned 0.99.2"},
		{"mismatch with --any-version", "0.99.1", "0.99.2", true, ""},
		{"nothing printed", "", "0.99.2", false, "version unknown, pinned 0.99.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckVersion(tt.line, tt.pinned, tt.anyVersion)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("err = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunSkipsACellWhoseVersionMismatches(t *testing.T) {
	cell := stubCell(t)
	cell.Version = func(context.Context) (string, error) { return "0.99.1", nil }

	reports := Run(t.Context(), []Cell{cell}, Options{Timeout: time.Minute})
	if r := reports[0]; !strings.Contains(r.Skipped, "version 0.99.1, pinned 0.99.2; pass --any-version") || len(r.Results) != 0 || r.Version != "0.99.1" {
		t.Errorf("report = %+v", r)
	}
	if Failures(reports) != 0 {
		t.Error("a skipped cell failed")
	}

	reports = Run(t.Context(), []Cell{cell}, Options{AnyVersion: true, Timeout: time.Minute})
	if r := reports[0]; r.Skipped != "" || len(r.Results) != len(Capabilities) || r.Version != "0.99.1 (pinned 0.99.2)" {
		t.Errorf("--any-version: report = %+v", r)
	}
}

func TestRunSkipsACellWhoseNeedFails(t *testing.T) {
	cell := stubCell(t)
	cell.Needs = []scenario.Need{{What: "LLAMA_BASE_URL set", Check: func(context.Context) error { return errors.New("LLAMA_BASE_URL is not set") }}}
	cell.Version = func(context.Context) (string, error) {
		t.Error("the version was asked for after a need failed")
		return "", nil
	}

	r := Run(t.Context(), []Cell{cell}, Options{Timeout: time.Minute})[0]
	if r.Skipped != "need LLAMA_BASE_URL set: LLAMA_BASE_URL is not set" || r.Version != "-" {
		t.Errorf("report = %+v", r)
	}
}

func TestRunOverTheStubDriver(t *testing.T) {
	var progress []string
	var events int
	reports := Run(t.Context(), []Cell{stubCell(t)}, Options{
		Timeout:  time.Minute,
		Progress: func(_ Cell, capability string) { progress = append(progress, capability) },
		Events:   func(Cell, string, harness.Event) { events++ },
	})
	r := reports[0]
	if r.Skipped != "" || len(r.Results) != len(Capabilities) {
		t.Fatalf("report = %+v", r)
	}
	if l := r.Latency; l == nil || l.Turn <= 0 || l.FirstText <= 0 || l.FirstText > l.Turn || l.Open <= 0 {
		t.Errorf("latency = %+v, want the exchange's open, first text, and turn", l)
	}
	// The stub streams and stops, so exchange and cancel pass; it can't answer a schema, so
	// every capability that needs a structured response fails, none of them stopping the rest.
	want := map[string]Status{
		"exchange": Pass, "cancel": Pass,
		"resume": Fail, "tool": Fail, "skill": Fail, "structured": Fail, "vision": Fail,
		"dropped-image": Pass, "audio-tool": NA,
	}
	for _, res := range r.Results {
		if res.Status != want[res.Capability] {
			t.Errorf("%s = %s (%s), want %s", res.Capability, res.Status, res.Reason, want[res.Capability])
		}
	}
	if got := r.Results[1].Reason; !strings.Contains(got, "aborted after 5 text deltas") {
		t.Errorf("cancel reason = %q", got)
	}
	if got := r.Results[7].Reason; !strings.Contains(got, harnesstest.Reply) {
		t.Errorf("dropped-image reason = %q, want the reply", got)
	}
	if !strings.Contains(r.Results[2].Reason, "structured response") {
		t.Errorf("resume reason = %q", r.Results[2].Reason)
	}
	if !strings.Contains(r.Results[8].Reason, "no direct model client") {
		t.Errorf("audio-tool reason = %q", r.Results[8].Reason)
	}
	if len(progress) != len(Capabilities)+1 || progress[0] != "" || events == 0 {
		t.Errorf("progress = %q, events = %d", progress, events)
	}
	if Failures(reports) != 5 {
		t.Errorf("failures = %d", Failures(reports))
	}
}

func TestACapabilityThatTimesOutFails(t *testing.T) {
	slow := Capability{Name: "slow", run: func(ctx context.Context, _ *env) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	res := runCapability(t.Context(), stubCell(t), slow, Options{Timeout: 10 * time.Millisecond}, &Latency{})
	if res.Status != Fail || !strings.Contains(res.Reason, "deadline exceeded") {
		t.Errorf("result = %+v", res)
	}
}

func TestDroppedImageNotApplicable(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Cell)
		wantN string
	}{
		{"harness that drops none", func(c *Cell) { c.Profile = scenario.Claude }, "drops no image"},
		{"default model takes images", func(c *Cell) { c.DefaultTakesImages = true }, "default model takes images"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cell := stubCell(t)
			tt.edit(&cell)
			e := &env{cell: cell, capability: "dropped-image"}
			status, reason := Capabilities[7].execute(t.Context(), e)
			if status != NA || !strings.Contains(reason, tt.wantN) {
				t.Errorf("status = %s, reason = %q", status, reason)
			}
		})
	}
}

func TestRender(t *testing.T) {
	pass := func(name string) Result { return Result{Capability: name, Status: Pass, Reason: "ok"} }
	var passing []Result
	for _, c := range Capabilities {
		passing = append(passing, pass(c.Name))
	}
	failing := append([]Result(nil), passing...)
	failing[2] = Result{Capability: "resume", Status: Fail, Reason: "the code word is \"x\",\nwant \"y\""}
	failing[7] = Result{Capability: "dropped-image", Status: NA, Reason: "the default model takes images"}

	reports := []Report{
		{Harness: "pi", Provider: "llama.cpp", Version: "0.99.2", Results: passing},
		{Harness: "pi", Provider: "azure", Version: "0.99.2", Results: failing},
		{Harness: "claude", Provider: "anthropic", Version: "-", Skipped: "need the harness executable on the PATH: not found"},
	}
	var buf bytes.Buffer
	Render(&buf, reports)
	want := `HARNESS  PROVIDER   VERSION  exchange  cancel  resume  tool  skill  structured  vision  dropped-image  audio-tool
pi       llama.cpp  0.99.2   pass      pass    pass    pass  pass   pass        pass    pass           pass
pi       azure      0.99.2   pass      pass    FAIL    pass  pass   pass        pass    n/a            pass
claude   anthropic  -        skip      skip    skip    skip  skip   skip        skip    skip           skip

Why not pass:
  pi/azure  FAIL  resume: the code word is "x", want "y"
  pi/azure  n/a  dropped-image: the default model takes images
  claude/anthropic  skip  need the harness executable on the PATH: not found

2 cells ran, 1 skipped; 16 pass, 1 FAIL, 1 n/a
`
	if got := buf.String(); got != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, want)
	}
	if Failures(reports) != 1 {
		t.Errorf("failures = %d", Failures(reports))
	}
}

func TestRenderLatency(t *testing.T) {
	reports := []Report{
		{Harness: "pi", Provider: "llama.cpp", Version: "0.99.2",
			Latency: &Latency{Open: 1300 * time.Millisecond, FirstText: 450 * time.Millisecond, Turn: 2 * time.Second}},
		{Harness: "claude", Provider: "anthropic", Version: "-", Skipped: "not found"},
	}
	var buf bytes.Buffer
	Render(&buf, reports)
	want := `
Latency of one plain exchange:
  HARNESS  PROVIDER   OPEN  FIRST TEXT  TURN
  pi       llama.cpp  1.3s  0.5s        2.0s
`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("rendered:\n%s\nwant it to hold:\n%s", buf.String(), want)
	}
}

func TestRenderOfAllPassingHasNoReasons(t *testing.T) {
	var results []Result
	for _, c := range Capabilities {
		results = append(results, Result{Capability: c.Name, Status: Pass})
	}
	var buf bytes.Buffer
	Render(&buf, []Report{{Harness: "pi", Provider: "llama.cpp", Version: "0.99.2", Results: results}})
	if strings.Contains(buf.String(), "Why not pass") {
		t.Errorf("rendered reasons for a clean run:\n%s", buf.String())
	}
}

// stubCell is a cell over the scripted harness: it streams and stops, and answers no schema.
func stubCell(t *testing.T) Cell {
	t.Helper()
	d := harnesstest.Driver{Stream: "essay"}
	return Cell{
		Harness:  "pi",
		Provider: "llama.cpp",
		Profile:  scenario.Pi,
		Pinned:   "0.99.2",
		Version:  func(context.Context) (string, error) { return "0.99.2", nil },
		Service: session.New(
			func() (harness.Driver, error) { return d, nil },
			func() harness.Options { return harness.Options{} },
		),
		VisionModel: "vision",
		Models:      func() (scenario.Models, error) { return scenario.Models{}, errors.New("no endpoint") },
	}
}
