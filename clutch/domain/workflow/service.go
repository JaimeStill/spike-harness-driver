package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/JaimeStill/spike-harness-driver/workflow"
)

// shutdownTimeout bounds stopping a run on an interrupt: cancelling its exchanges and closing
// its sessions.
const shutdownTimeout = 30 * time.Second

// Service runs workflows on the harnesses their sessions name.
type Service struct {
	runner func(limit int) (*workflow.Runner, error)
}

// New returns a Service over runner, which builds a Runner that bounds the exchanges in flight
// to limit, zero for none. It is called once per command, after the flags are parsed.
func New(runner func(limit int) (*workflow.Runner, error)) *Service {
	return &Service{runner: runner}
}

// Runner returns a Runner bounding the exchanges in flight to limit.
func (s *Service) Runner(limit int) (*workflow.Runner, error) { return s.runner(limit) }

// Load reads and validates the workflow file at path.
func Load(path string) (workflow.Workflow, error) {
	f, err := os.Open(path)
	if err != nil {
		return workflow.Workflow{}, err
	}
	defer func() { _ = f.Close() }()
	return workflow.Load(f)
}

// Follow passes each of the run's events with a Seq above after to fn as it happens, until the
// run's last event, and returns the run's state then. When ctx ends first, Follow shuts the
// Runner down, which stops the run without ending it, and keeps following until the Runner
// stops running the run. The state it returns is then unfinished, and the error is ctx's.
func Follow(ctx context.Context, r *workflow.Runner, id string, after int, fn func(workflow.Event)) (workflow.State, error) {
	// The subscription outlives ctx, so the events of a shutdown still arrive.
	events, err := r.Subscribe(context.WithoutCancel(ctx), id, after)
	if err != nil {
		return workflow.State{}, err
	}
	stopped := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() {
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		stopped <- r.Shutdown(sctx)
	})
	for e := range events {
		fn(e)
	}
	s, err := r.Wait(context.WithoutCancel(ctx), id)
	if !stop() {
		err = errors.Join(err, <-stopped, ctx.Err())
	}
	if err != nil && errors.Is(err, workflow.ErrShutdown) {
		err = errors.Join(ctx.Err(), fmt.Errorf("run %s stopped unfinished", id))
	}
	return s, err
}
