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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
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

// ErrNoStructuredResponse is an exchange's error when its request carried a schema and the
// exchange ended, other than by cancellation, without a structured response.
var ErrNoStructuredResponse = errors.New("harness: the exchange ended without a structured response")

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
	// Tools are the tools the session offers the model beyond the harness's own. The harness
	// calls each one back in this process.
	Tools []Tool
	// Skills are the skills the session makes available to the model.
	Skills []Skill
	// HarnessTools names the harness's own tools the session enables, such as "read". Nil
	// keeps the harness's default set; empty enables none.
	HarnessTools []string
}

// ToolHandler runs one call of a tool with the model's arguments, which the harness has
// validated against the tool's schema. Its result is the text the model receives. An error
// reaches the model as a failed tool call; the exchange carries on.
//
// A handler must return once ctx ends, which it does when the exchange is cancelled or the
// session closes. A session waits only a bounded time for a handler still running as it
// closes, and then abandons it.
type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

// Tool is a tool the program defines and runs. It may come from a Go library that packages
// it, or from an external source such as a command tool (harness/catalog).
type Tool struct {
	// Name is how the model calls the tool.
	Name string
	// Description tells the model what the tool does and when to call it.
	Description string
	// Schema is the JSON Schema of the tool's arguments, an object schema.
	Schema  json.RawMessage
	Handler ToolHandler
}

// toolName is the rule the model providers share for a tool's name.
var toolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Validate checks that t can be offered to a model: a name of 1 to 64 letters, digits,
// underscores, and hyphens, which also keeps it safe in an adapter's comma-separated lists, and
// a handler. A tool that declares a schema must declare an object schema.
func (t Tool) Validate() error {
	switch {
	case !toolName.MatchString(t.Name):
		return fmt.Errorf("harness: tool %q: the name isn't 1 to 64 letters, digits, underscores, and hyphens", t.Name)
	case t.Handler == nil:
		return fmt.Errorf("harness: tool %q has no handler", t.Name)
	}
	if len(t.Schema) > 0 {
		if err := ValidateSchema(t.Schema); err != nil {
			return fmt.Errorf("harness: tool %q: %w", t.Name, err)
		}
	}
	return nil
}

// ErrInvalidSchema is returned, wrapped, for a schema that isn't a JSON object whose type is
// "object", the only kind a tool's arguments or a structured response take.
var ErrInvalidSchema = errors.New("harness: invalid schema")

// ValidateSchema checks that schema is a JSON object whose type is "object". It checks no
// more of JSON Schema than that: the harness validates values against the schema.
func ValidateSchema(schema json.RawMessage) error {
	var s struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("%w: not a JSON object: %w", ErrInvalidSchema, err)
	}
	switch t := string(bytes.TrimSpace(s.Type)); {
	case t == "":
		return fmt.Errorf(`%w: it declares no type, want "object"`, ErrInvalidSchema)
	case t != `"object"`:
		return fmt.Errorf(`%w: its type is %s, want "object"`, ErrInvalidSchema, t)
	}
	return nil
}

// Skill is a skill: a directory holding a SKILL.md, whose frontmatter names and describes it,
// and any files the skill refers to. FS is rooted at that directory, so a skill may live on
// disk (os.DirFS) or be embedded in a Go library (fs.Sub of an embed.FS).
type Skill struct {
	// Name is the skill's name, as its SKILL.md frontmatter gives it.
	Name string
	FS   fs.FS
	// Dir, when set, is the directory on disk FS reads. An adapter whose harness reads skills
	// from disk passes it as it is, so the model sees the skill where it lives; without it,
	// the adapter writes FS out somewhere of its own.
	Dir string
}

// Request is the payload of one exchange.
type Request struct {
	Text string `json:"text"`
	// Schema, when set, is the JSON Schema of the structured response the exchange must
	// produce, an object schema. The harness validates the response against it, and the
	// Result carries it in Structured.
	Schema json.RawMessage `json:"schema,omitempty"`
}

// Result summarizes a finished exchange.
type Result struct {
	// StopReason is the harness's stop reason for the last assistant message, such as "stop",
	// "aborted", or "error".
	StopReason string `json:"stopReason,omitempty"`
	// Text is the concatenated text of the last assistant message.
	Text string `json:"text,omitempty"`
	// Structured is the structured response, for a request that carried a schema.
	Structured json.RawMessage `json:"structured,omitempty"`
	Usage      Usage           `json:"usage"`
}

// Usage counts the tokens an exchange's last assistant message used. Input excludes tokens
// read from or written to the provider's prompt cache, which CacheRead and CacheWrite count,
// so with a warm cache Input alone understates the prompt.
type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
}
