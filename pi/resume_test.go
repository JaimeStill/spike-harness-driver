package pi

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
)

// openAt opens the session id on a fake Pi that keeps its sessions in dir, recording in store.
func openAt(t *testing.T, dir, id string, store harness.Store) (*harness.Session, error) {
	t.Helper()
	d := fakeDriver("ok")
	d.SessionDir = filepath.Join(dir, "pi")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return d.Open(ctx, harness.Options{Provider: "llama.cpp", Model: "m", SessionID: id, Store: store})
}

// exchange runs one plain exchange on s to its end.
func exchange(t *testing.T, s *harness.Session) *harness.Exchange {
	t.Helper()
	x, err := s.Send(t.Context(), harness.Request{Text: "what is Go?"})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, x, 0, nil)
	if _, err := x.Wait(); err != nil {
		t.Fatal(err)
	}
	return x
}

func TestResumeKeepsTheSessionAndItsExchangeIDs(t *testing.T) {
	dir := t.TempDir()
	store := filestore.New(filepath.Join(dir, "exchanges"))

	first, err := openAt(t, dir, "resume-1", store)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != "resume-1" {
		t.Fatalf("session ID = %q, want resume-1", first.ID())
	}
	x1 := exchange(t, first)
	closeSession(t, first)

	// A second Pi process, and a new Store over the same directory, as after a restart.
	store = filestore.New(filepath.Join(dir, "exchanges"))
	second, err := openAt(t, dir, "resume-1", store)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(t, second)
	x2 := exchange(t, second)

	recs, err := second.Exchanges(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].ExchangeID != x1.ID() || recs[1].ExchangeID != x2.ID() {
		t.Fatalf("records = %+v, want exchanges %s then %s", recs, x1.ID(), x2.ID())
	}
	// Each exchange is bound to the entries its run appended: the session's system message,
	// user, and assistant for the first, user and assistant for the second. The entries that
	// setting the model appended on each open belong to neither.
	entries, kinds := readEntries(t, filepath.Join(dir, "pi", "resume-1.entries"))
	want := [][]string{{"system", "user", "assistant"}, {"user", "assistant"}}
	for i, r := range recs {
		at := slices.Index(entries, r.Entries[0])
		if at < 0 || !slices.Equal(entries[at:at+len(r.Entries)], r.Entries) {
			t.Fatalf("record %d entries %v are not a span of Pi's %v", i, r.Entries, entries)
		}
		if got := kinds[at : at+len(r.Entries)]; !slices.Equal(got, want[i]) {
			t.Errorf("record %d binds %v, want %v", i, got, want[i])
		}
	}
	if len(entries) != 9 {
		t.Errorf("Pi holds %d entries, want 9: 3 on the first set_model, 3 and 2 for the runs, 1 on the second set_model", len(entries))
	}
}

func TestResumingALostSessionIsAMismatch(t *testing.T) {
	dir := t.TempDir()
	store := filestore.New(filepath.Join(dir, "exchanges"))
	s, err := openAt(t, dir, "lost-1", store)
	if err != nil {
		t.Fatal(err)
	}
	exchange(t, s)
	closeSession(t, s)

	if err := os.Remove(filepath.Join(dir, "pi", "lost-1.entries")); err != nil {
		t.Fatal(err)
	}
	if s, err := openAt(t, dir, "lost-1", store); !errors.Is(err, harness.ErrJournalMismatch) {
		if s != nil {
			closeSession(t, s)
		}
		t.Fatalf("Open = %v, want ErrJournalMismatch", err)
	}
}

// TestEntryIDsFromCapturedResponses reads the get_entries responses captured from Pi 0.87.1:
// a new session after set_model, the same session after one exchange, the entries since the
// first, and a since that names no entry.
func TestEntryIDsFromCapturedResponses(t *testing.T) {
	lines := readLines(t, "entries.jsonl")
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4", len(lines))
	}
	want := [][]string{
		{"161ecb91", "90e8c4fc", "13ac5caf"},
		{"161ecb91", "90e8c4fc", "13ac5caf", "91637f9d", "936a807d", "a44212ac"},
		{"90e8c4fc", "13ac5caf", "91637f9d", "936a807d", "a44212ac"},
	}
	for i, line := range lines {
		f, err := codec{}.Decode(line)
		if err != nil || f.Response == nil {
			t.Fatalf("line %d: %v, %+v", i, err, f)
		}
		if i == 3 {
			var failed *commandError
			if !errors.As(f.Response.Err, &failed) || failed.Message != "Entry not found: nope" {
				t.Errorf("line 3 error = %v", f.Response.Err)
			}
			continue
		}
		ids, err := entryIDs(f.Response.Data)
		if err != nil || !slices.Equal(ids, want[i]) {
			t.Errorf("line %d ids = %v, %v; want %v", i, ids, err, want[i])
		}
	}
}

// readEntries reads the fake Pi's persisted journal: each entry's ID and kind.
func readEntries(t *testing.T, path string) (ids, kinds []string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for s := bufio.NewScanner(f); s.Scan(); {
		id, kind, _ := strings.Cut(s.Text(), " ")
		ids, kinds = append(ids, id), append(kinds, kind)
	}
	return ids, kinds
}
