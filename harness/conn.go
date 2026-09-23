package harness

import "context"

// Conn is an adapter's live connection to one harness session. It is the whole of what an
// adapter supplies; Session builds exchanges on top of it.
type Conn interface {
	// Prompt submits req and returns once the harness has accepted it. The run's outcome
	// arrives as events.
	Prompt(ctx context.Context, req Request) error
	// Cancel asks the harness to stop the running prompt. The harness still reports the end
	// of the run through events, ending with EventEnded.
	Cancel(ctx context.Context) error
	// Events yields the harness's normalized events, without SessionID, ExchangeID, or Seq,
	// ending each run with EventEnded. It must be drained promptly and closes when the harness
	// exits; an unexpected exit is announced by a final EventError.
	Events() <-chan Event
	// Close ends the harness, which closes Events.
	Close() error
}
