package scenario_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	var opened []harness.Options
	svc := session.New(
		func() (harness.Driver, error) {
			return harnesstest.Driver{Stream: "essay", Opened: func(o harness.Options) { opened = append(opened, o) }}, nil
		},
		func() harness.Options { return harness.Options{Store: store} },
	)
	// The stub harness replies with the same text whatever it is asked, calls no tools, and
	// gives no structured response, so the scenarios that check the model's reply fail at
	// their first check.
	fails := map[string]string{
		"tool":       "step 2: the model never called the lookup_code tool",
		"skill":      "step 1: the reply lacks the motto",
		"structured": "step 2: exchange ended in error: " + harness.ErrNoStructuredResponse.Error(),
	}
	scenarios := scenario.Scenarios(svc, nil)
	for _, s := range scenarios {
		t.Run(s.Name, func(t *testing.T) {
			opened = nil
			rep, out := reporter()
			cmd := scenario.Command(s, func() *scenario.Reporter { return rep })
			cmd.SetArgs(nil)
			err := cmd.ExecuteContext(t.Context())
			if want, ok := fails[s.Name]; ok {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("Run = %v, want an error containing %q\n%s", err, want, out.String())
				}
			} else if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "session "+harnesstest.SessionID) || !strings.Contains(out.String(), "result: stop=") {
				t.Errorf("narration:\n%s", out.String())
			}
			if len(opened) == 0 {
				t.Fatal("no session opened")
			}
			switch first := opened[0]; s.Name {
			case "tool":
				if len(first.Tools) != 1 || first.Tools[0].Name != "lookup_code" || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened with tools %+v, harness tools %#v", first.Tools, first.HarnessTools)
				}
			case "skill":
				if len(first.Skills) != 1 || first.Skills[0].Name != "clutch-motto" || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened with skills %+v, harness tools %#v", first.Skills, first.HarnessTools)
				}
			case "structured":
				if len(first.Tools) != 0 || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened with tools %+v, harness tools %#v", first.Tools, first.HarnessTools)
				}
				// The store records each exchange's request, so it shows the schema was sent.
				recs, err := store.Records(t.Context(), harnesstest.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(recs, func(r harness.Record) bool {
					return json.Valid(r.Request.Schema) && strings.Contains(string(r.Request.Schema), `"city"`)
				}) {
					t.Errorf("no recorded request carries the capital schema: %+v", recs)
				}
			}
		})
	}

	var listing bytes.Buffer
	scenario.WriteListing(&listing, scenarios)
	for _, name := range []string{"exchange", "cancel", "resume", "tool", "skill", "structured"} {
		if !strings.Contains(listing.String(), name) {
			t.Errorf("listing lacks %s:\n%s", name, listing.String())
		}
	}
}
