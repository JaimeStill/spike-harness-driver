package filestore_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
)

func record(session string, entries ...string) harness.Record {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return harness.Record{
		SessionID:  session,
		ExchangeID: uuid.NewV7(),
		Request:    harness.Request{Text: "What is Go?\nIn one line."},
		Result:     harness.Result{StopReason: "stop", Text: "A language.", Usage: harness.Usage{Input: 3, Output: 4}},
		Entries:    entries,
		Started:    now,
		Ended:      now.Add(time.Second),
	}
}

func TestRecordsComeBackInOrderFromANewStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exchanges")
	s := filestore.New(dir)
	want := []harness.Record{record("s1", "e1", "e2"), record("s1", "e3")}
	want[1].Err = "boom"
	other := record("s2")
	for _, r := range append(want, other) {
		if err := s.Put(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := filestore.New(dir).Records(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Records =\n%+v\nwant\n%+v", got, want)
	}
}

func TestASessionWithNoRecords(t *testing.T) {
	got, err := filestore.New(t.TempDir()).Records(t.Context(), "none")
	if got != nil || err != nil {
		t.Errorf("Records = %v, %v", got, err)
	}
}

func TestInvalidSessionIDs(t *testing.T) {
	dir := t.TempDir()
	s := filestore.New(filepath.Join(dir, "store"))
	for _, id := range []string{"", ".", "..", "../escape", `a\b`} {
		if err := s.Put(t.Context(), record(id)); err == nil {
			t.Errorf("Put(%q) succeeded", id)
		}
		if _, err := s.Records(t.Context(), id); err == nil {
			t.Errorf("Records(%q) succeeded", id)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %v", entries)
	}
}
