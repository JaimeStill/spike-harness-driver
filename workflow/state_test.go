package workflow_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/workflow"
)

func TestFold(t *testing.T) {
	w := workflow.Workflow{
		Name:     "w",
		Sessions: []workflow.SessionSpec{{Name: "s"}},
		Steps:    []workflow.Step{{ID: "a", Session: "s", Prompt: "go"}, {ID: "b", Session: "s", Prompt: "go"}},
	}
	x := uuid.NewV7()
	now := time.Now()
	events := []workflow.Event{
		{Kind: workflow.KindRunStarted, Workflow: &w},
		{Kind: workflow.KindSessionOpened, Session: "s", SessionID: "h1"},
		{Kind: workflow.KindStepStarted, Step: "a", ExchangeID: x, Time: now},
		{Kind: workflow.KindStepEnded, Step: "a", Status: workflow.StatusDone, Result: &harness.Result{Text: "ok"}},
		{Kind: workflow.KindStepStarted, Step: "b"},
		{Kind: workflow.KindRunPaused, Until: now},
		{Kind: workflow.KindRunResumed},
	}
	for i := range events {
		events[i].RunID, events[i].Seq = "r", i+1
	}
	s, err := workflow.Fold(events)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != workflow.StatusRunning || s.Seq != 7 || s.Sessions["s"] != "h1" {
		t.Errorf("state = %+v", s)
	}
	if a := s.Steps["a"]; a.Status != workflow.StatusDone || a.ExchangeID != x || a.Result.Text != "ok" {
		t.Errorf("step a = %+v", a)
	}
	if b := s.Steps["b"]; b.Status != workflow.StatusRunning {
		t.Errorf("step b = %+v", b)
	}
	if s.Done() != 1 || len(s.Results()) != 1 {
		t.Errorf("Done = %d, Results = %v", s.Done(), s.Results())
	}

	end := workflow.Event{RunID: "r", Seq: 8, Kind: workflow.KindRunEnded, Status: workflow.StatusCancelled}
	if err := s.Apply(end); err != nil || s.Status != workflow.StatusCancelled {
		t.Fatalf("Apply(run_ended): %v, status %s", err, s.Status)
	}
	after := workflow.Event{RunID: "r", Seq: 9, Kind: workflow.KindRunResumed}
	if err := s.Apply(after); !errors.Is(err, workflow.ErrLog) {
		t.Errorf("an event after run_ended: err = %v", err)
	}
}

func TestFoldInconsistent(t *testing.T) {
	w := workflow.Workflow{Name: "w", Sessions: []workflow.SessionSpec{{Name: "s"}}, Steps: []workflow.Step{{ID: "a", Session: "s", Prompt: "go"}}}
	start := workflow.Event{RunID: "r", Seq: 1, Kind: workflow.KindRunStarted, Workflow: &w}
	tests := []struct {
		name   string
		events []workflow.Event
	}{
		{"before start", []workflow.Event{{RunID: "r", Seq: 1, Kind: workflow.KindRunResumed}}},
		{"gap", []workflow.Event{start, {RunID: "r", Seq: 3, Kind: workflow.KindRunResumed}}},
		{"other run", []workflow.Event{start, {RunID: "q", Seq: 2, Kind: workflow.KindRunResumed}}},
		{"unknown step", []workflow.Event{start, {RunID: "r", Seq: 2, Kind: workflow.KindStepStarted, Step: "z"}}},
		{"unknown session", []workflow.Event{start, {RunID: "r", Seq: 2, Kind: workflow.KindSessionOpened, Session: "z"}}},
		{"live event", []workflow.Event{start, {RunID: "r", Kind: workflow.KindExchange}}},
		{"unended status", []workflow.Event{start, {RunID: "r", Seq: 2, Kind: workflow.KindStepEnded, Step: "a", Status: workflow.StatusRunning}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := workflow.Fold(tt.events); !errors.Is(err, workflow.ErrLog) {
				t.Fatalf("err = %v, want ErrLog", err)
			}
		})
	}
}
