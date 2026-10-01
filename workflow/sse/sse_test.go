package sse_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
	"github.com/JaimeStill/spike-harness-driver/workflow"
	"github.com/JaimeStill/spike-harness-driver/workflow/sse"
)

// store is an in-memory workflow.Store.
type store struct {
	mu   sync.Mutex
	logs map[string][]workflow.Event
}

func (s *store) Append(_ context.Context, e workflow.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs[e.RunID] = append(s.logs[e.RunID], e)
	return nil
}

func (s *store) Events(_ context.Context, id string, after int) ([]workflow.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []workflow.Event
	for _, e := range s.logs[id] {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *store) Runs(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.logs {
		ids = append(ids, id)
	}
	return ids, nil
}

func newRunner(t *testing.T, d harnesstest.Driver) *workflow.Runner {
	t.Helper()
	open := func(ctx context.Context, spec workflow.SessionSpec, _ string) (*harness.Session, error) {
		return d.Open(ctx, harness.Options{SessionID: spec.Name})
	}
	r := workflow.NewRunner(open, &store{logs: map[string][]workflow.Event{}}, 0)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return r
}

func start(t *testing.T, r *workflow.Runner, prompt string) string {
	t.Helper()
	id, err := r.Start(context.Background(), workflow.Workflow{
		Name:     "demo",
		Sessions: []workflow.SessionSpec{{Name: "a"}},
		Steps:    []workflow.Step{{ID: "one", Session: "a", Prompt: prompt}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func finished(t *testing.T) (*workflow.Runner, string) {
	t.Helper()
	r := newRunner(t, harnesstest.Driver{})
	id := start(t, r, "Say hi.")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	return r, id
}

// frame is one parsed message of a stream.
type frame struct{ id, event, data string }

// parse reads a whole stream back into its frames, dropping comments.
func parse(t *testing.T, body string) []frame {
	t.Helper()
	var frames []frame
	var cur frame
	for line := range strings.SplitSeq(body, "\n") {
		switch {
		case line == "":
			if cur != (frame{}) {
				frames = append(frames, cur)
			}
			cur = frame{}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		default:
			t.Fatalf("unexpected line %q", line)
		}
	}
	return frames
}

func get(t *testing.T, r *workflow.Runner, id, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	sse.Stream(rec, req, r, id, sse.Options{})
	return rec
}

func ids(frames []frame) []int {
	var out []int
	for _, f := range frames {
		n, err := strconv.Atoi(f.id)
		if err != nil {
			out = append(out, -1)
			continue
		}
		out = append(out, n)
	}
	return out
}

func TestStreamFinishedRun(t *testing.T) {
	r, id := finished(t)
	rec := get(t, r, id, "/events", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-cache" || h.Get("X-Accel-Buffering") != "no" {
		t.Errorf("headers = %v", h)
	}
	frames := parse(t, rec.Body.String())
	if len(frames) < 4 {
		t.Fatalf("frames = %+v", frames)
	}
	for i, f := range frames {
		if f.id != strconv.Itoa(i+1) {
			t.Fatalf("frame %d id = %q; want %d", i, f.id, i+1)
		}
		if !strings.Contains(f.data, `"kind":"`+f.event+`"`) {
			t.Errorf("frame %d: event %q, data %s", i, f.event, f.data)
		}
	}
	if frames[0].event != "run_started" || frames[len(frames)-1].event != "run_ended" {
		t.Errorf("first %q, last %q", frames[0].event, frames[len(frames)-1].event)
	}
}

func TestStreamCursor(t *testing.T) {
	r, id := finished(t)
	all := parse(t, get(t, r, id, "/events", nil).Body.String())
	want := ids(all)[2:]

	for name, c := range map[string]struct {
		target string
		header map[string]string
	}{
		"header": {"/events", map[string]string{"Last-Event-ID": "2"}},
		"query":  {"/events?after=2", nil},
		// The header wins over the query.
		"both": {"/events?after=999", map[string]string{"Last-Event-ID": "2"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := get(t, r, id, c.target, c.header)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := ids(parse(t, rec.Body.String())); !slices.Equal(got, want) {
				t.Errorf("ids = %v; want %v", got, want)
			}
		})
	}
}

func TestStreamErrors(t *testing.T) {
	r, id := finished(t)
	for name, c := range map[string]struct {
		run, target string
		header      map[string]string
		code        int
	}{
		"unknown run":     {"nope", "/events", nil, http.StatusNotFound},
		"bad header":      {id, "/events", map[string]string{"Last-Event-ID": "x"}, http.StatusBadRequest},
		"bad query":       {id, "/events?after=abc", nil, http.StatusBadRequest},
		"negative cursor": {id, "/events?after=-1", nil, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := get(t, r, c.run, c.target, c.header)
			if rec.Code != c.code {
				t.Errorf("status = %d; want %d", rec.Code, c.code)
			}
			if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				t.Errorf("Content-Type = %q", ct)
			}
		})
	}
}

func TestStreamLiveAndKeepalive(t *testing.T) {
	r := newRunner(t, harnesstest.Driver{Stream: "long"})
	id := start(t, r, "a long one")

	returned := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer close(returned)
		sse.Stream(w, req, r, id, sse.Options{Keepalive: 20 * time.Millisecond})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The step streams until cancelled, so the run stays in flight: read until the stream has
	// shown a live exchange event and a keepalive.
	var prev string
	var live, keepalive bool
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() && (!live || !keepalive) {
		line := sc.Text()
		switch line {
		case "event: exchange":
			live = true
			// A live event has no id line.
			if strings.HasPrefix(prev, "id: ") {
				t.Error("exchange event carries an id")
			}
		case ": keepalive":
			keepalive = true
		}
		prev = line
	}
	if !live || !keepalive {
		t.Fatalf("live = %v, keepalive = %v, err = %v", live, keepalive, sc.Err())
	}

	cancel()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("Stream did not return after the request ended")
	}
}
