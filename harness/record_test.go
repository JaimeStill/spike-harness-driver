package harness_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// memStore is a Store in memory. putErr, when set, fails every Put.
type memStore struct {
	mu      sync.Mutex
	records map[string][]harness.Record
	putErr  error
}

func newMemStore() *memStore { return &memStore{records: map[string][]harness.Record{}} }

func (m *memStore) Put(_ context.Context, rec harness.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return m.putErr
	}
	m.records[rec.SessionID] = append(m.records[rec.SessionID], rec)
	return nil
}

func (m *memStore) Records(_ context.Context, id string) ([]harness.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.records[id]), nil
}

// journalConnection is a fakeConnection that keeps a Journal the test appends to by hand.
type journalConnection struct {
	*fakeConnection
	mu         sync.Mutex
	entries    []string
	sinceCalls int
}

func newJournalConnection(entries ...string) *journalConnection {
	return &journalConnection{fakeConnection: newFakeConnection(), entries: entries}
}

func (c *journalConnection) append(ids ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, ids...)
}

func (c *journalConnection) Head(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return "", nil
	}
	return c.entries[len(c.entries)-1], nil
}

func (c *journalConnection) Since(_ context.Context, id string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sinceCalls++
	if id == "" {
		return slices.Clone(c.entries), nil
	}
	i := slices.Index(c.entries, id)
	if i < 0 {
		return nil, fmt.Errorf("since %s: %w", id, harness.ErrUnknownEntry)
	}
	return slices.Clone(c.entries[i+1:]), nil
}

// run sends one exchange, appends ids to the journal as the harness would during the run,
// ends the run with stop, and waits for the exchange.
func run(t *testing.T, s *harness.Session, c *journalConnection, stop string, ids ...string) *harness.Exchange {
	t.Helper()
	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.append(ids...)
	c.finish(stop, "answer")
	<-events
	return x
}

func TestExchangesAreRecordedWithTheirEntries(t *testing.T) {
	c := newJournalConnection("e0")
	store := newMemStore()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()

	x1 := run(t, s, c, "stop", "e1", "e2")
	// Wait returns only once the record exists.
	if _, err := x1.Wait(); err != nil {
		t.Fatal(err)
	}
	recs, err := s.Exchanges(t.Context())
	if err != nil || len(recs) != 1 {
		t.Fatalf("after one exchange: %v, %v", recs, err)
	}

	x2 := run(t, s, c, "stop", "e3")
	recs, err = s.Exchanges(t.Context())
	if err != nil || len(recs) != 2 {
		t.Fatalf("after two exchanges: %v, %v", recs, err)
	}
	for i, want := range []struct {
		x       *harness.Exchange
		entries []string
	}{{x1, []string{"e1", "e2"}}, {x2, []string{"e3"}}} {
		r := recs[i]
		if r.ExchangeID != want.x.ID() || r.SessionID != "s1" || !slices.Equal(r.Entries, want.entries) {
			t.Errorf("record %d = %s %s %v, want %s s1 %v", i, r.SessionID, r.ExchangeID, r.Entries, want.x.ID(), want.entries)
		}
		if r.Request.Text != "hi" || r.Result.StopReason != "stop" || r.Result.Text != "answer" || r.Err != "" {
			t.Errorf("record %d = %+v", i, r)
		}
		if r.Started.IsZero() || r.Ended.Before(r.Started) {
			t.Errorf("record %d times: %v to %v", i, r.Started, r.Ended)
		}
	}
}

func TestACancelledExchangeIsRecorded(t *testing.T) {
	c := newJournalConnection()
	store := newMemStore()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	x.Cancel()
	<-c.cancels
	c.append("u1", "a1")
	c.finish("aborted", "")
	<-events

	recs, _ := s.Exchanges(t.Context())
	if len(recs) != 1 || recs[0].Result.StopReason != "aborted" || !slices.Equal(recs[0].Entries, []string{"u1", "a1"}) {
		t.Fatalf("records = %+v", recs)
	}
}

func TestResumeChecksTheJournal(t *testing.T) {
	store := newMemStore()
	prior := harness.Record{SessionID: "s1", Entries: []string{"e1"}}
	if err := store.Put(t.Context(), prior); err != nil {
		t.Fatal(err)
	}

	t.Run("entries held", func(t *testing.T) {
		c := newJournalConnection("e1")
		s := newSession(t, c, store)
		defer func() { _ = s.Close() }()
		run(t, s, c, "stop", "e2")
		recs, _ := s.Exchanges(t.Context())
		if len(recs) != 2 || !slices.Equal(recs[1].Entries, []string{"e2"}) {
			t.Fatalf("records = %+v", recs)
		}
	})
	t.Run("entries lost", func(t *testing.T) {
		c := newJournalConnection("other")
		_, err := harness.NewSession(t.Context(), "s1", c, store)
		if !errors.Is(err, harness.ErrJournalMismatch) {
			t.Fatalf("NewSession = %v, want ErrJournalMismatch", err)
		}
	})
}

func TestNoStoreRecordsNothing(t *testing.T) {
	c := newJournalConnection()
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()
	run(t, s, c, "stop", "e1")
	recs, err := s.Exchanges(t.Context())
	if recs != nil || err != nil {
		t.Errorf("Exchanges = %v, %v", recs, err)
	}
	if c.sinceCalls != 0 {
		t.Errorf("Since called %d times without a store", c.sinceCalls)
	}
}

func TestAnExitRecordsNoEntries(t *testing.T) {
	c := newJournalConnection()
	store := newMemStore()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.append("u1")
	c.events <- harness.Event{Kind: harness.EventError, Err: "boom"}
	_ = c.Close()
	<-events

	recs, _ := s.Exchanges(t.Context())
	if len(recs) != 1 || recs[0].Entries != nil || recs[0].Err != "boom" {
		t.Fatalf("records = %+v", recs)
	}
}

func TestAStoreFailureIsTheExchangesError(t *testing.T) {
	c := newJournalConnection()
	store := newMemStore()
	store.putErr = errors.New("disk full")
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()

	x := run(t, s, c, "stop", "e1")
	if _, err := x.Wait(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Wait = %v, want the store's error", err)
	}
	// The session carries on: the next exchange can be sent.
	store.mu.Lock()
	store.putErr = nil
	store.mu.Unlock()
	run(t, s, c, "stop", "e2")
	recs, _ := s.Exchanges(t.Context())
	if len(recs) != 1 || !slices.Equal(recs[0].Entries, []string{"e2"}) {
		t.Fatalf("records = %+v", recs)
	}
}
