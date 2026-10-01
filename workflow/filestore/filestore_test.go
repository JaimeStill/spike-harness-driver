package filestore_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/workflow"
	"github.com/JaimeStill/spike-harness-driver/workflow/filestore"
)

func event(run string, seq int, kind workflow.Kind) workflow.Event {
	return workflow.Event{RunID: run, Seq: seq, Kind: kind, Time: time.Now().UTC().Truncate(time.Millisecond)}
}

// events is a run's log: started with a workflow, a step started, a step ended with a result.
func events(run string) []workflow.Event {
	started := event(run, 1, workflow.KindRunStarted)
	started.Workflow = &workflow.Workflow{
		Name:     "demo",
		Sessions: []workflow.SessionSpec{{Name: "a"}},
		Steps:    []workflow.Step{{ID: "one", Session: "a", Prompt: "Say hi.\nTwice."}},
	}
	begun := event(run, 2, workflow.KindStepStarted)
	begun.Step, begun.Session, begun.ExchangeID, begun.Prompt = "one", "a", uuid.NewV7(), "Say hi.\nTwice."
	ended := event(run, 3, workflow.KindStepEnded)
	ended.Step, ended.Session, ended.ExchangeID, ended.Status = "one", "a", begun.ExchangeID, workflow.StatusDone
	ended.Result = &harness.Result{StopReason: "stop", Text: "hi hi", Usage: harness.Usage{Input: 3, Output: 4}}
	return []workflow.Event{started, begun, ended}
}

func appendAll(t *testing.T, s *filestore.Store, evs ...workflow.Event) {
	t.Helper()
	for _, e := range evs {
		if err := s.Append(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEventsComeBackInOrderFromANewStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	want := events("r1")
	appendAll(t, filestore.New(dir), want...)
	appendAll(t, filestore.New(dir), event("r2", 1, workflow.KindRunStarted))

	got, err := filestore.New(dir).Events(t.Context(), "r1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Events =\n%+v\nwant\n%+v", got, want)
	}
}

func TestEventsAfterASeq(t *testing.T) {
	s := filestore.New(t.TempDir())
	want := events("r1")
	appendAll(t, s, want...)

	for after, n := range map[int]int{0: 3, 1: 2, 2: 1, 3: 0, 9: 0} {
		got, err := s.Events(t.Context(), "r1", after)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n || (n > 0 && !reflect.DeepEqual(got, want[3-n:])) {
			t.Errorf("Events after %d = %+v, want the last %d", after, got, n)
		}
	}
}

func TestARunWithNoEvents(t *testing.T) {
	got, err := filestore.New(t.TempDir()).Events(t.Context(), "none", 0)
	if got != nil || err != nil {
		t.Errorf("Events = %v, %v", got, err)
	}
}

func TestRunsListsTheLogsSorted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	s := filestore.New(dir)
	if ids, err := s.Runs(t.Context()); ids != nil || err != nil {
		t.Fatalf("Runs of a missing dir = %v, %v", ids, err)
	}
	appendAll(t, s, event("b", 1, workflow.KindRunStarted), event("a", 1, workflow.KindRunStarted), event("c", 1, workflow.KindRunStarted))
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Runs = %v, want %v", got, want)
	}
}

func TestInvalidRunIDs(t *testing.T) {
	dir := t.TempDir()
	s := filestore.New(filepath.Join(dir, "store"))
	for _, id := range []string{"", "../escape", "a/b", `a\b`} {
		if err := s.Append(t.Context(), event(id, 1, workflow.KindRunStarted)); err == nil {
			t.Errorf("Append(%q) succeeded", id)
		}
		if _, err := s.Events(t.Context(), id, 0); err == nil {
			t.Errorf("Events(%q) succeeded", id)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %v", entries)
	}
}

func TestTheDirectoryAndFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	appendAll(t, filestore.New(dir), event("r1", 1, workflow.KindRunStarted))
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "r1.jsonl"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestATornFinalLineIsIgnoredAndRepairedByTheNextAppend(t *testing.T) {
	dir := t.TempDir()
	s := filestore.New(dir)
	evs := events("r1")
	appendAll(t, s, evs[:2]...)
	path := filepath.Join(dir, "r1.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"runId":"r1","seq":3,"ki`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	got, err := s.Events(t.Context(), "r1", 0)
	if err != nil {
		t.Fatalf("Events with a torn tail: %v", err)
	}
	if !reflect.DeepEqual(got, evs[:2]) {
		t.Errorf("Events = %+v, want the first two", got)
	}

	appendAll(t, s, evs[2])
	got, err = s.Events(t.Context(), "r1", 0)
	if err != nil {
		t.Fatalf("Events after repair: %v", err)
	}
	if !reflect.DeepEqual(got, evs) {
		t.Errorf("Events = %+v, want %+v", got, evs)
	}
}

func TestATornOnlyLineIsRepairedToo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r1.jsonl"), []byte(`{"runId"`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := filestore.New(dir)
	if got, err := s.Events(t.Context(), "r1", 0); got != nil || err != nil {
		t.Fatalf("Events = %v, %v", got, err)
	}
	e := event("r1", 1, workflow.KindRunStarted)
	appendAll(t, s, e)
	got, err := s.Events(t.Context(), "r1", 0)
	if err != nil || !reflect.DeepEqual(got, []workflow.Event{e}) {
		t.Errorf("Events = %+v, %v", got, err)
	}
}

func TestACorruptMiddleLineIsAnError(t *testing.T) {
	dir := t.TempDir()
	s := filestore.New(dir)
	evs := events("r1")
	appendAll(t, s, evs[0])
	f, err := os.OpenFile(filepath.Join(dir, "r1.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	appendAll(t, s, evs[1])

	if _, err := s.Events(t.Context(), "r1", 0); err == nil {
		t.Error("Events with a corrupt middle line succeeded")
	}

	// A corrupt final line that ends in a newline is not a torn write, so it is an error too.
	f, _ = os.OpenFile(filepath.Join(dir, "r2.jsonl"), os.O_WRONLY|os.O_CREATE, 0o600)
	_, _ = f.WriteString("not json\n")
	_ = f.Close()
	if _, err := s.Events(t.Context(), "r2", 0); err == nil {
		t.Error("Events with a corrupt terminated final line succeeded")
	}
}
