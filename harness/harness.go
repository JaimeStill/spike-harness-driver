// Package harness defines the harness-agnostic surface a Go program uses to drive an external
// agent harness: a Driver opens a Session, and each Send on the session opens one Exchange, a
// request-and-response block whose events are tagged with the session and exchange IDs.
package harness

import (
	"context"
	"errors"
)

// ErrBusy is returned by Send while an earlier exchange on the same session is still open.
// Harnesses such as Pi reject a new prompt during a run, so a session carries one exchange at
// a time.
var ErrBusy = errors.New("harness: session has an open exchange")

// ErrClosed is returned by Send after the session is closed.
var ErrClosed = errors.New("harness: session is closed")

// Driver opens sessions on one harness.
type Driver interface {
	Open(ctx context.Context, opts Options) (Session, error)
}

// Options selects the model a session runs against.
type Options struct {
	// Provider names the harness's model provider, such as "llama.cpp" or "anthropic".
	Provider string
	// Model is the provider's model ID.
	Model string
	// Dir is the harness's working directory. Empty means the current directory.
	Dir string
}

// Session is one conversation with the harness that spans several exchanges.
type Session interface {
	// ID is the harness's own session ID.
	ID() string
	// Send opens an exchange for req. Cancelling ctx cancels the exchange.
	Send(ctx context.Context, req Request) (Exchange, error)
	// Close ends the session and stops the harness.
	Close() error
}

// Request is the payload of one exchange.
type Request struct {
	Text string
}

// Exchange is one request-and-response block within a session.
type Exchange interface {
	// ID is the exchange ID the driver assigned.
	ID() string
	// Events streams the exchange's events in order. The channel closes after the KindEnded
	// event.
	Events() <-chan Event
	// Cancel asks the harness to stop the exchange. It returns at once; the exchange still
	// ends with KindCancelled and KindEnded events.
	Cancel()
	// Wait blocks until the exchange ends and returns its result. The error is non-nil when
	// the exchange ended in an error rather than a stop or a cancellation.
	Wait() (Result, error)
}

// Result summarizes a finished exchange.
type Result struct {
	// StopReason is the harness's stop reason for the last assistant message, such as "stop",
	// "aborted", or "error".
	StopReason string
	// Text is the concatenated text of the last assistant message.
	Text  string
	Usage Usage
}

// Usage counts the tokens an exchange used.
type Usage struct {
	Input  int
	Output int
}
