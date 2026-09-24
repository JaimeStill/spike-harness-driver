// Package harness is the harness-agnostic surface a Go program uses to drive an external agent
// harness. A Driver opens a Session over an adapter's Connection, and each Send on the session
// opens one Exchange, a request-and-response block whose events are tagged with the session and
// exchange IDs.
//
// An adapter supplies only the Connection: how to prompt, how to cancel, and the harness's events
// normalized to Event. Scoping events to exchanges, sequencing them, and folding them into a
// Result are the same for every harness and live here.
//
// A session outlives its harness process when the harness persists it: a Driver opens it
// again by Options.SessionID. Exchange IDs are the driver's own, so a session keeps them in a
// Store, one Record per ended exchange. When the adapter's Connection also keeps a Journal,
// each Record names the harness entries its exchange appended, which binds the driver's ID
// to the harness's durable record and lets a resumed session check that the harness still
// holds what was recorded.
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

// ErrUnknownEntry is returned, wrapped, by Journal.Since for an entry ID the harness doesn't
// hold.
var ErrUnknownEntry = errors.New("harness: unknown entry")

// ErrJournalMismatch is returned when a session is opened whose recorded exchanges name an
// entry the harness no longer holds, as when the harness lost the session and started a
// fresh one under the same ID.
var ErrJournalMismatch = errors.New("harness: the harness no longer holds the session's recorded entries")

// Driver opens sessions on one harness.
type Driver interface {
	Open(ctx context.Context, opts Options) (*Session, error)
}

// Options selects the session to open and the model it runs against.
type Options struct {
	// SessionID opens the harness session with that ID, creating it if the harness has none.
	// Empty opens a new session under an ID the harness assigns.
	SessionID string
	// Store keeps the session's exchange records. Nil keeps none.
	Store Store
	// Provider names the harness's model provider, such as "llama.cpp" or "anthropic".
	Provider string
	// Model is the provider's model ID.
	Model string
	// Dir is the harness's working directory. Empty means the current directory.
	Dir string
}

// Request is the payload of one exchange.
type Request struct {
	Text string `json:"text"`
}

// Result summarizes a finished exchange.
type Result struct {
	// StopReason is the harness's stop reason for the last assistant message, such as "stop",
	// "aborted", or "error".
	StopReason string `json:"stopReason,omitempty"`
	// Text is the concatenated text of the last assistant message.
	Text  string `json:"text,omitempty"`
	Usage Usage  `json:"usage"`
}

// Usage counts the tokens an exchange used.
type Usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}
