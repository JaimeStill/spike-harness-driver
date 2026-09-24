package harness_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// memStore is a Store in memory. putErr, when set, fails every Put. putGate, when set, holds
// each Put until it closes.
type memStore struct {
	mu      sync.Mutex
	records map[string][]harness.Record
	putErr  error
	putGate chan struct{}
	puts    atomic.Int32 // Puts begun
}

// putsWaiting reports how many Puts have begun.
func (m *memStore) putsWaiting() int32 { return m.puts.Load() }

// holdPuts holds every Put until the returned release is called. The test defers release
// before the session's Close, so a failing assertion can't leave Close waiting on a Put.
func (m *memStore) holdPuts() (release func()) {
	m.putGate = make(chan struct{})
	var once sync.Once
	return func() { once.Do(func() { close(m.putGate) }) }
}

func newMemStore() *memStore { return &memStore{records: map[string][]harness.Record{}} }

func (m *memStore) Put(_ context.Context, rec harness.Record) error {
	m.puts.Add(1)
	if m.putGate != nil {
		<-m.putGate
	}
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
// sinceErr, when set, fails the next Since; sinceGate, when set, holds Since until it closes
// or its context ends.
type journalConnection struct {
	*fakeConnection
	mu         sync.Mutex
	entries    []string
	sinceCalls int
	sinceErr   error
	sinceGate  chan struct{}
	sinceBegun atomic.Int32
}

// sinceCallsWaiting reports how many Since calls have begun.
func (c *journalConnection) sinceCallsWaiting() int32 { return c.sinceBegun.Load() }

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

func (c *journalConnection) Since(ctx context.Context, id string) ([]string, error) {
	c.sinceBegun.Add(1)
	if err := gate(ctx, c.sinceGate); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sinceCalls++
	if err := c.sinceErr; err != nil {
		c.sinceErr = nil
		return nil, err
	}
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

func TestWaitReturnsOnlyOnceTheRecordIsStored(t *testing.T) {
	c := newJournalConnection()
	store := newMemStore()
	release := store.holdPuts()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()
	defer release()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.append("e1")
	c.finish("stop", "answer")

	waited := make(chan struct{})
	go func() {
		_, _ = x.Wait()
		close(waited)
	}()
	never(t, waited, "Wait returned before the record was stored")
	never(t, events, "the exchange's events ended before the record was stored")

	release()
	<-waited
	<-events
	if recs, _ := s.Exchanges(t.Context()); len(recs) != 1 || recs[0].ExchangeID != x.ID() {
		t.Fatalf("records = %+v", recs)
	}
}

func TestAFailedBindingIsRecordedAndTheCursorResyncs(t *testing.T) {
	c := newJournalConnection("e0")
	store := newMemStore()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()

	c.sinceErr = errors.New("journal unreachable")
	x1 := run(t, s, c, "stop", "e1", "e2")
	if _, err := x1.Wait(); err == nil || !strings.Contains(err.Error(), "journal unreachable") {
		t.Fatalf("Wait = %v, want the binding error", err)
	}
	x2 := run(t, s, c, "stop", "e3")

	recs, _ := s.Exchanges(t.Context())
	if len(recs) != 2 {
		t.Fatalf("records = %+v", recs)
	}
	if recs[0].ExchangeID != x1.ID() || recs[0].Entries != nil || !strings.Contains(recs[0].Err, "journal unreachable") {
		t.Errorf("first record = %+v, want no entries and the binding error", recs[0])
	}
	// The second exchange claims only its own entry, not the first's unbound ones.
	if recs[1].ExchangeID != x2.ID() || !slices.Equal(recs[1].Entries, []string{"e3"}) {
		t.Errorf("second record = %+v, want entries [e3]", recs[1])
	}
}

func TestCloseDuringRecordingStillStoresTheRecord(t *testing.T) {
	c := newJournalConnection()
	c.sinceGate = make(chan struct{}) // never opens: the journal call waits until Close
	store := newMemStore()
	s := newSession(t, c, store)

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.append("e1")
	c.finish("stop", "answer")
	for c.sinceCallsWaiting() == 0 {
		runtime.Gosched()
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	<-events

	recs, _ := store.Records(t.Context(), "s1")
	if len(recs) != 1 || recs[0].ExchangeID != x.ID() || recs[0].Entries != nil || recs[0].Err == "" {
		t.Fatalf("records = %+v, want the exchange recorded with the binding error", recs)
	}
}

func TestACancelAfterTheRunEndsReachesNoHarness(t *testing.T) {
	c := newJournalConnection()
	store := newMemStore()
	release := store.holdPuts()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()
	defer release()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.finish("stop", "answer")
	// EventEnded has arrived and the record is held: the run is over, but x is still open.
	for store.putsWaiting() == 0 {
		runtime.Gosched()
	}
	x.Cancel()
	never(t, c.cancels, "Cancel reached the harness after the run ended")
	release()
	<-events
	if _, err := x.Wait(); err != nil {
		t.Fatalf("Wait = %v", err)
	}
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
	c.exitErr = errors.New("boom")
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
