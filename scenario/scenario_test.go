package scenario_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/output"
	"github.com/JaimeStill/spike-harness-driver/scenario"
)

func reporter() (*scenario.Reporter, *bytes.Buffer) {
	var out bytes.Buffer
	return scenario.NewReporter(output.New(&out, &bytes.Buffer{}, nil)), &out
}

func TestRunNarratesStepsAndCleansUp(t *testing.T) {
	var ran []string
	cleaned := false
	s := scenario.Scenario{
		Name: "demo",
		Steps: func() ([]scenario.Step, func() error) {
			step := func(name string, err error) scenario.Step {
				return scenario.Step{Intent: name, Action: func(context.Context, *scenario.Reporter) error {
					ran = append(ran, name)
					return err
				}}
			}
			return []scenario.Step{step("one", nil), step("two", errors.New("boom")), step("three", nil)},
				func() error { cleaned = true; return nil }
		},
	}
	rep, out := reporter()
	err := scenario.Run(t.Context(), s, rep)
	if err == nil || !strings.Contains(err.Error(), "demo: step 2: boom") {
		t.Fatalf("Run = %v", err)
	}
	if strings.Join(ran, ",") != "one,two" || !cleaned {
		t.Errorf("ran %v, cleaned %v", ran, cleaned)
	}
	if !strings.Contains(out.String(), "[1/3] one") || !strings.Contains(out.String(), "[2/3] two") {
		t.Errorf("narration:\n%s", out.String())
	}
}

func TestRunStopsAtAFailedNeed(t *testing.T) {
	built := false
	s := scenario.Scenario{
		Name:  "demo",
		Needs: []scenario.Need{{What: "a harness", Check: func(context.Context) error { return errors.New("missing") }}},
		Steps: func() ([]scenario.Step, func() error) { built = true; return nil, nil },
	}
	rep, _ := reporter()
	err := scenario.Run(t.Context(), s, rep)
	if err == nil || !strings.Contains(err.Error(), "need a harness: missing") || built {
		t.Errorf("Run = %v, built = %v", err, built)
	}
}

func TestScenariosOverAStubHarness(t *testing.T) {
	svc := session.New(
		func() (harness.Driver, error) { return stubDriver{}, nil },
		func() harness.Options { return harness.Options{} },
	)
	scenarios := scenario.Scenarios(svc, nil)
	for _, s := range scenarios {
		t.Run(s.Name, func(t *testing.T) {
			rep, out := reporter()
			cmd := scenario.Command(s, func() *scenario.Reporter { return rep })
			cmd.SetArgs(nil)
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "session stub-session") || !strings.Contains(out.String(), "result: stop=") {
				t.Errorf("narration:\n%s", out.String())
			}
		})
	}

	var listing bytes.Buffer
	scenario.WriteListing(&listing, scenarios)
	for _, name := range []string{"exchange", "cancel"} {
		if !strings.Contains(listing.String(), name) {
			t.Errorf("listing lacks %s:\n%s", name, listing.String())
		}
	}
}

// stubDriver opens sessions whose harness streams text deltas until the prompt is short
// enough to answer, or until it is cancelled.
type stubDriver struct{}

func (stubDriver) Open(context.Context, harness.Options) (*harness.Session, error) {
	c := &stubConnection{events: make(chan harness.Event, 64), cancel: make(chan struct{})}
	return harness.NewSession("stub-session", c), nil
}

type stubConnection struct {
	events    chan harness.Event
	cancel    chan struct{}
	closeOnce sync.Once
}

func (c *stubConnection) Prompt(_ context.Context, req harness.Request) error {
	long := strings.Contains(req.Text, "essay")
	go func() {
		c.events <- harness.Event{Kind: harness.EventStarted}
		for i := 0; !long && i < 3 || long; i++ {
			select {
			case <-c.cancel:
				c.events <- harness.Event{Kind: harness.EventMessageEnd, StopReason: "aborted"}
				c.events <- harness.Event{Kind: harness.EventCancelled, StopReason: "aborted"}
				c.events <- harness.Event{Kind: harness.EventEnded}
				return
			case c.events <- harness.Event{Kind: harness.EventTextDelta, Text: "."}:
			}
		}
		c.events <- harness.Event{Kind: harness.EventMessageEnd, Text: "...", StopReason: "stop"}
		c.events <- harness.Event{Kind: harness.EventEnded}
	}()
	return nil
}

func (c *stubConnection) Cancel(context.Context) error {
	close(c.cancel)
	return nil
}

func (c *stubConnection) Events() <-chan harness.Event { return c.events }

func (c *stubConnection) Close() error {
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}
