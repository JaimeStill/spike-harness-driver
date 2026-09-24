package scenario_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
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
	store := filestore.New(t.TempDir())
	svc := session.New(
		func() (harness.Driver, error) { return harnesstest.Driver{Stream: "essay"}, nil },
		func() harness.Options { return harness.Options{Store: store} },
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
			if !strings.Contains(out.String(), "session "+harnesstest.SessionID) || !strings.Contains(out.String(), "result: stop=") {
				t.Errorf("narration:\n%s", out.String())
			}
		})
	}

	var listing bytes.Buffer
	scenario.WriteListing(&listing, scenarios)
	for _, name := range []string{"exchange", "cancel", "resume"} {
		if !strings.Contains(listing.String(), name) {
			t.Errorf("listing lacks %s:\n%s", name, listing.String())
		}
	}
}
