package pi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

const plainText = "Go is a statically typed, compiled programming language designed for simplicity, concurrency, and efficient performance."

func open(t *testing.T, mode string) *harness.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := fakeDriver(mode).Open(ctx, harness.Options{Provider: "llama.cpp", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func closeSession(t *testing.T, s *harness.Session) {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// drain collects an exchange's events until the channel closes. With stopAfter > 0 it calls
// onDelta after that many text deltas.
func drain(t *testing.T, x *harness.Exchange, stopAfter int, onDelta func()) []harness.Event {
	t.Helper()
	var events []harness.Event
	deltas := 0
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-x.Events():
			if !ok {
				return events
			}
			events = append(events, ev)
			if ev.Kind == harness.EventTextDelta {
				if deltas++; deltas == stopAfter {
					onDelta()
				}
			}
		case <-timeout:
			t.Fatalf("exchange %s did not end; got %d events", x.ID(), len(events))
		}
	}
}

// checkScoped verifies that every event carries the session and exchange IDs, that sequence
// numbers run from 1 without gaps, and that the exchange ends with EventEnded.
func checkScoped(t *testing.T, s *harness.Session, x *harness.Exchange, events []harness.Event) {
	t.Helper()
	for i, ev := range events {
		if ev.SessionID != s.ID() || ev.ExchangeID != x.ID() || ev.Seq != i+1 {
			t.Fatalf("event %d = {%s %s %d}, want {%s %s %d}",
				i, ev.SessionID, ev.ExchangeID, ev.Seq, s.ID(), x.ID(), i+1)
		}
	}
	if k := events[len(events)-1].Kind; k != harness.EventEnded {
		t.Fatalf("last event is %s, want ended", k)
	}
}

func has(events []harness.Event, k harness.EventKind) bool {
	for _, ev := range events {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

func TestTwoExchangesInOneSession(t *testing.T) {
	s := open(t, "ok")
	if s.ID() != "fake-session" {
		t.Fatalf("session ID = %q, want Pi's sessionId", s.ID())
	}
	var ids []uuid.UUID
	for range 2 {
		x, err := s.Send(t.Context(), harness.Request{Text: "what is Go?"})
		if err != nil {
			t.Fatal(err)
		}
		events := drain(t, x, 0, nil)
		checkScoped(t, s, x, events)
		res, err := x.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != "stop" || res.Text != plainText || res.Usage.Output == 0 {
			t.Fatalf("result = %+v", res)
		}
		if end := events[len(events)-1]; end.StopReason != "stop" {
			t.Fatalf("ended StopReason = %q, want stop", end.StopReason)
		}
		ids = append(ids, x.ID())
	}
	if ids[0] == ids[1] {
		t.Fatalf("both exchanges have ID %s", ids[0])
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.Send(t.Context(), harness.Request{Text: "again"}); !errors.Is(err, harness.ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
}

func TestPromptCarriesImages(t *testing.T) {
	file := filepath.Join(t.TempDir(), "prompts.jsonl")
	d := fakeDriver("ok")
	d.Env = append(d.Env, promptsEnv+"="+file)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := d.Open(ctx, harness.Options{Provider: "llama.cpp", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(t, s)

	png := []byte("\x89PNG image bytes")
	x, err := s.Send(t.Context(), harness.Request{
		Text:   "What is in this image?",
		Images: []harness.Image{{MediaType: "image/png", Data: png}},
	})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, x, 0, nil)
	if _, err := x.Wait(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Message string            `json:"message"`
		Images  []json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("prompt line %s: %v", raw, err)
	}
	want := `{"type":"image","data":"` + base64.StdEncoding.EncodeToString(png) +
		`","mimeType":"image/png"}`
	if got.Message != "What is in this image?" || len(got.Images) != 1 || string(got.Images[0]) != want {
		t.Fatalf("Pi was prompted with %s, want the message and the image %s", raw, want)
	}
}

func TestRefreshModelsRunsBeforePiStarts(t *testing.T) {
	dir := t.TempDir()
	refreshes, launch := filepath.Join(dir, "refreshes"), filepath.Join(dir, "launch.json")
	d := fakeDriver("ok")
	d.RefreshModels = true
	d.Env = append(d.Env, refreshesEnv+"="+refreshes, launchEnv+"="+launch)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := d.Open(ctx, harness.Options{Provider: "llama.cpp", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	closeSession(t, s)
	got, err := os.ReadFile(refreshes)
	if err != nil || string(got) != "refresh\n" {
		t.Fatalf("refreshes = %q, %v; want one refresh", got, err)
	}
	if _, err := os.Stat(launch); err != nil {
		t.Fatalf("Pi didn't start after the refresh: %v", err)
	}
}

func TestAFailedRefreshFailsOpen(t *testing.T) {
	launch := filepath.Join(t.TempDir(), "launch.json")
	d := fakeDriver("refreshfail")
	d.RefreshModels = true
	d.Env = append(d.Env, launchEnv+"="+launch)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := d.Open(ctx, harness.Options{Provider: "llama.cpp", Model: "m"})
	if err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded, want the refresh's error")
	}
	if !strings.Contains(err.Error(), "update --models") || !strings.Contains(err.Error(), "fetch failed") {
		t.Fatalf("Open = %v, want the refresh's command and Pi's message", err)
	}
	if _, err := os.Stat(launch); !os.IsNotExist(err) {
		t.Fatalf("Pi started after a failed refresh: %v", err)
	}
}

func TestSendWhileOpenIsBusy(t *testing.T) {
	s := open(t, "ok")
	defer closeSession(t, s)
	x, err := s.Send(t.Context(), harness.Request{Text: longPrompt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(t.Context(), harness.Request{Text: "second"}); !errors.Is(err, harness.ErrBusy) {
		t.Fatalf("second Send = %v, want ErrBusy", err)
	}
	x.Cancel()
	drain(t, x, 0, nil)
}

func TestCancelMidStream(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(x *harness.Exchange, stop context.CancelFunc)
	}{
		{name: "Cancel", cancel: func(x *harness.Exchange, _ context.CancelFunc) { x.Cancel() }},
		{name: "context", cancel: func(_ *harness.Exchange, stop context.CancelFunc) { stop() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t, "ok")
			defer closeSession(t, s)
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			x, err := s.Send(ctx, harness.Request{Text: longPrompt})
			if err != nil {
				t.Fatal(err)
			}
			events := drain(t, x, 3, func() { tt.cancel(x, stop) })
			checkScoped(t, s, x, events)
			if !has(events, harness.EventCancelled) {
				t.Fatal("no cancelled event")
			}
			res, err := x.Wait()
			if err != nil || res.StopReason != "aborted" {
				t.Fatalf("Wait = %+v, %v; want aborted, nil", res, err)
			}

			// The session stays usable after a cancellation.
			x2, err := s.Send(t.Context(), harness.Request{Text: "what is Go?"})
			if err != nil {
				t.Fatal(err)
			}
			checkScoped(t, s, x2, drain(t, x2, 0, nil))
		})
	}
}

func TestPiExitEndsTheExchange(t *testing.T) {
	s := open(t, "crash")
	x, err := s.Send(t.Context(), harness.Request{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, x, 0, nil)
	checkScoped(t, s, x, events)
	if _, err := x.Wait(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Wait error = %v, want Pi's stderr", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("Close after a crash returned nil")
	}
}
