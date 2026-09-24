package harness

import "context"

// Connection is an adapter's live connection to one harness session. It is the whole of what an
// adapter supplies; Session builds exchanges on top of it.
type Connection interface {
	// Prompt submits req and returns once the harness has accepted it. The run's outcome
	// arrives as events.
	Prompt(ctx context.Context, req Request) error
	// Cancel asks the harness to stop the running prompt. The harness still reports the end
	// of the run through events, ending with EventEnded.
	Cancel(ctx context.Context) error
	// Events yields the harness's normalized events, without SessionID, ExchangeID, or Seq,
	// ending each run with EventEnded. It must be drained promptly and closes when the harness
	// exits.
	Events() <-chan Event
	// Err reports why the harness exited, once Events has closed: nil when Close ended it,
	// and the exit error otherwise. An error event a run reported before the exit is the
	// run's, not the harness's.
	Err() error
	// Close ends the harness, which closes Events.
	Close() error
}

// Journal is the harness's durable record of a session: an append-only log of entries with
// stable IDs that outlive the harness process. A Connection implements it when its harness
// keeps one, and the session binds each exchange to the entries it appended.
type Journal interface {
	// Head returns the ID of the session's last entry, or "" for a session with none.
	Head(ctx context.Context) (string, error)
	// Since returns the IDs of the entries after id in append order, or every entry's ID when
	// id is "". It fails with ErrUnknownEntry when the harness holds no entry id.
	Since(ctx context.Context, id string) ([]string, error)
}
