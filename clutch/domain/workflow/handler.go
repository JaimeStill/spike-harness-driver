package workflow

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/JaimeStill/spike-harness-driver/workflow"
	"github.com/JaimeStill/spike-harness-driver/workflow/sse"
)

// maxWorkflow bounds a posted workflow's body.
const maxWorkflow = 1 << 20

// Handler serves the runner's runs over HTTP:
//
//	POST   /runs              start a run of the workflow in the body; 201 with {"id": ...}
//	GET    /runs              every run's state, oldest first
//	GET    /runs/{id}         one run's state
//	GET    /runs/{id}/events  the run's events as Server-Sent Events, after Last-Event-ID
//	DELETE /runs/{id}         cancel the run; 202
//
// A run outlives the request that started it. The runner runs it until it ends, and a client
// follows it, or follows it again after a disconnect, through its events.
func Handler(r *workflow.Runner, opts sse.Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", func(w http.ResponseWriter, req *http.Request) {
		wf, err := workflow.Load(http.MaxBytesReader(w, req.Body, maxWorkflow))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, err := r.Start(req.Context(), wf)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Location", "/runs/"+id)
		reply(w, http.StatusCreated, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /runs", func(w http.ResponseWriter, req *http.Request) {
		states, err := r.Runs(req.Context())
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusOK, states)
	})
	mux.HandleFunc("GET /runs/{id}", func(w http.ResponseWriter, req *http.Request) {
		s, err := r.State(req.Context(), req.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusOK, s)
	})
	mux.HandleFunc("GET /runs/{id}/events", func(w http.ResponseWriter, req *http.Request) {
		sse.Stream(w, req, r, req.PathValue("id"), opts)
	})
	mux.HandleFunc("DELETE /runs/{id}", func(w http.ResponseWriter, req *http.Request) {
		if err := r.Cancel(req.Context(), req.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

// fail writes err with the status its kind calls for.
func fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, workflow.ErrUnknownRun):
		status = http.StatusNotFound
	case errors.Is(err, workflow.ErrRunEnded):
		status = http.StatusConflict
	case errors.Is(err, workflow.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, workflow.ErrShutdown):
		status = http.StatusServiceUnavailable
	}
	http.Error(w, err.Error(), status)
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
