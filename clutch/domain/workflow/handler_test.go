package workflow_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	wfdomain "github.com/JaimeStill/spike-harness-driver/clutch/domain/workflow"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/workflow"
	"github.com/JaimeStill/spike-harness-driver/workflow/sse"
)

func workflowBody(t *testing.T, second string) string {
	t.Helper()
	b, err := os.ReadFile(writeWorkflow(t, second))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func post(t *testing.T, base, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(base+"/runs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var v struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v.ID
}

func do(t *testing.T, method, url string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// events reads an event stream until it closes, returning each event's kind, after calling
// seen with each logged event.
func events(t *testing.T, url string, header http.Header, seen func(workflow.Event)) []workflow.Event {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var logged []workflow.Event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var e workflow.Event
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatal(err)
		}
		if e.Logged() {
			logged = append(logged, e)
			if seen != nil {
				seen(e)
			}
		}
	}
	return logged
}

func TestHandler(t *testing.T) {
	svc := newService(t.TempDir(), true)
	r, _ := svc.Runner(0)
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	srv := httptest.NewServer(wfdomain.Handler(r, sse.Options{}))
	defer srv.Close()

	if code, _ := post(t, srv.URL, `{"name": "x"}`); code != http.StatusBadRequest {
		t.Errorf("an invalid workflow: %d", code)
	}
	code, id := post(t, srv.URL, workflowBody(t, "then"))
	if code != http.StatusCreated || id == "" {
		t.Fatalf("POST: %d %q", code, id)
	}
	all := events(t, srv.URL+"/runs/"+id+"/events", nil, nil)
	if last := all[len(all)-1]; last.Kind != workflow.KindRunEnded || last.Status != workflow.StatusDone {
		t.Fatalf("last event %+v", last)
	}
	// A reconnect picks up after the last event it saw.
	tail := events(t, srv.URL+"/runs/"+id+"/events", http.Header{"Last-Event-Id": {fmt.Sprint(len(all) - 2)}}, nil)
	if len(tail) != 2 || tail[0].Seq != len(all)-1 {
		t.Errorf("replay after %d: %+v", len(all)-2, tail)
	}
	if code := do(t, http.MethodDelete, srv.URL+"/runs/"+id); code != http.StatusConflict {
		t.Errorf("DELETE an ended run: %d", code)
	}
	if code := do(t, http.MethodGet, srv.URL+"/runs/nope"); code != http.StatusNotFound {
		t.Errorf("GET an unknown run: %d", code)
	}

	// A live run cancels: its stream ends with the run cancelled.
	_, id = post(t, srv.URL, workflowBody(t, longPrompt))
	got := events(t, srv.URL+"/runs/"+id+"/events", nil, func(e workflow.Event) {
		if e.Kind == workflow.KindStepStarted && e.Step == "second" {
			if code := do(t, http.MethodDelete, srv.URL+"/runs/"+id); code != http.StatusAccepted {
				t.Errorf("DELETE a live run: %d", code)
			}
		}
	})
	if last := got[len(got)-1]; last.Kind != workflow.KindRunEnded || last.Status != workflow.StatusCancelled {
		t.Errorf("last event %+v", last)
	}

	resp, err := http.Get(srv.URL + "/runs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var listing struct {
		Runs   []workflow.State
		Errors []string
	}
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil || len(listing.Runs) != 2 || listing.Errors != nil {
		t.Errorf("GET /runs: %v, %+v", err, listing)
	}
	// A run ID that can't name a log is an unknown run, and an ended run's caught-up stream is
	// 204, so an EventSource stops reconnecting.
	if code := do(t, http.MethodGet, srv.URL+"/runs/..%2Fescape"); code != http.StatusNotFound {
		t.Errorf("GET an invalid run ID: %d", code)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/runs/"+id+"/events", nil)
	req.Header.Set("Last-Event-ID", fmt.Sprint(got[len(got)-1].Seq))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Errorf("a caught-up stream of an ended run: %v, %v", err, resp)
	} else {
		_ = resp.Body.Close()
	}
}

// syncBuffer is a Buffer the server's goroutine writes while the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var serving = regexp.MustCompile(`serving on (http://\S+)`)

// startServe runs "serve" on a free port until the returned stop is called, and returns its
// base URL and output.
func startServe(t *testing.T, svc *wfdomain.Service) (string, *syncBuffer, func()) {
	t.Helper()
	out := &syncBuffer{}
	cmd := wfdomain.ServeCommand(svc, output.New(out, out, nil))
	cmd.SetArgs([]string{"--addr", "127.0.0.1:0"})
	cmd.SetOut(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := serving.FindStringSubmatch(out.String()); m != nil {
			return m[1], out, func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("serve: %v", err)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("serve didn't start:\n%s", out)
	return "", nil, nil
}

func TestServeResumesAfterARestart(t *testing.T) {
	state := t.TempDir()
	base, _, stop := startServe(t, newService(state, true))
	_, id := post(t, base, workflowBody(t, longPrompt))
	var seq int
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil && seq == 0 {
		resp, err := http.Get(base + "/runs/" + id)
		if err != nil {
			t.Fatal(err)
		}
		var s workflow.State
		_ = json.NewDecoder(resp.Body).Decode(&s)
		_ = resp.Body.Close()
		if s.Steps["second"].Status == workflow.StatusRunning {
			seq = s.Seq
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	// The next serve, whose harness answers, resumes the run, and a client picks its stream up
	// where it left off.
	base, out, stop := startServe(t, newService(state, false))
	defer stop()
	if !strings.Contains(out.String(), "resumed run "+id) {
		t.Errorf("serve output:\n%s", out)
	}
	got := events(t, base+"/runs/"+id+"/events", http.Header{"Last-Event-Id": {fmt.Sprint(seq)}}, nil)
	var kinds []workflow.Kind
	for _, e := range got {
		kinds = append(kinds, e.Kind)
	}
	want := []workflow.Kind{workflow.KindRunResumed, workflow.KindStepStarted, workflow.KindStepEnded, workflow.KindRunEnded}
	if fmt.Sprint(kinds) != fmt.Sprint(want) || got[0].Seq != seq+1 {
		t.Errorf("events after %d: %v", seq, got)
	}
}
