// Package harness is the harness-agnostic surface a Go program uses to drive an external agent
// harness. A Driver opens a Session over an adapter's Connection, and each Send on the session opens
// one Exchange, a request-and-response block whose events are tagged with the session and
// exchange IDs.
//
// An adapter supplies only the Connection: how to prompt, how to cancel, and the harness's events
// normalized to Event. Scoping events to exchanges, sequencing them, and folding them into a
// Result are the same for every harness and live here.
package harness

import (
	"context"
	"errors"
)

// ErrBusy is returned by Send while an earlier exchange on the same session is still open.
// Harnesses such as Pi don't tag their events with the request they belong to, so a session
// carries one exchange at a time.
var ErrBusy = errors.New("harness: session has an open exchange")

// ErrClosed is returned by Send after the session is closed.
var ErrClosed = errors.New("harness: session is closed")

// Driver opens sessions on one harness.
type Driver interface {
	Open(ctx context.Context, opts Options) (*Session, error)
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

// Request is the payload of one exchange.
type Request struct {
	Text string
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
