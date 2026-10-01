package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/JaimeStill/spike-harness-driver/workflow"
)

// DefaultKeepalive is the Keepalive an Options with none takes.
const DefaultKeepalive = 15 * time.Second

// Options configures Stream.
type Options struct {
	// Keepalive is how often a comment line is written, so proxies don't close a stream
	// that goes idle. Zero takes DefaultKeepalive.
	Keepalive time.Duration
}

// Stream serves the run's events as a Server-Sent Events stream, starting after the request's
// cursor. The cursor is the Last-Event-ID header, which a reconnecting client sends on its own,
// else the after query parameter, else the start of the log. It returns when the run's last event
// has been written, when the runner stops following the run, when the request ends, or when a
// write fails.
//
// Stream reports errors as plain-text HTTP errors, which is possible only before the stream
// starts: a bad cursor is a 400, an unknown run a 404, and any other error from the runner a
// 500.
func Stream(w http.ResponseWriter, r *http.Request, runner *workflow.Runner, runID string, opts Options) {
	after, err := cursor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	events, err := runner.Subscribe(r.Context(), runID, after)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, workflow.ErrUnknownRun) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	// Tells nginx-style proxies to pass events through as they come rather than buffer them.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if rc.Flush() != nil {
		return
	}

	keepalive := opts.Keepalive
	if keepalive <= 0 {
		keepalive = DefaultKeepalive
	}
	// The ticker runs whether or not events flow. A comment among busy events costs little, and
	// no quiet stretch of the stream outlasts one period.
	tick := time.NewTicker(keepalive)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			if err := write(w, e); err != nil {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// cursor reads the request's position in the log: zero when it names none.
func cursor(r *http.Request) (int, error) {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("sse: cursor %q is not a non-negative integer", v)
	}
	return n, nil
}

// write frames one event. A logged event is named by its kind and carries its Seq as the id; a
// live exchange event carries none, so the client's last id always names a logged event. The
// JSON is on one line, so the data needs no splitting.
func write(w http.ResponseWriter, e workflow.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if e.Logged() {
		_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, data)
	} else {
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", workflow.KindExchange, data)
	}
	return err
}
