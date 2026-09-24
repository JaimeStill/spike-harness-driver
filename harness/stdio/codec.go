package stdio

import (
	"context"
	"encoding/json"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Codec translates between a harness's line protocol and the harness package. It is where an
// adapter's normalization lives.
type Codec interface {
	// Encode renders cmd as one line, stamped with the correlation id the harness will echo
	// in its response.
	Encode(id string, cmd any) ([]byte, error)
	// Decode parses one line into a response, a request from the harness, or the normalized
	// events it carries. A line that carries no normalized meaning still yields an
	// EventHarness, so nothing is lost.
	Decode(line []byte) (Frame, error)
	// Reply renders the answer to the harness's request req as one line.
	Reply(req Request, answer any) ([]byte, error)
}

// Frame is one decoded line: a response to a command, a request from the harness, or events.
type Frame struct {
	Response *Response
	Request  *Request
	Events   []harness.Event
}

// Request is one the harness makes of its driver and waits on, such as a call to a tool the
// driver runs. It is the reverse of a command: the harness asks, and the driver answers.
type Request struct {
	// ID is the harness's own id for the request, which the answer carries back.
	ID string
	// Body is the request as the codec decoded it, for the adapter's Handler to read.
	Body any
}

// Handler answers the harness's requests. The Client calls it on a goroutine of its own for
// each request, so a slow answer holds up neither the harness's other output nor other
// requests. ctx ends when the harness process does.
type Handler func(ctx context.Context, req Request) (answer any)

// Response answers one command.
type Response struct {
	// ID is the correlation id the command was encoded with.
	ID string
	// Err is non-nil when the harness reported that the command failed.
	Err  error
	Data json.RawMessage
}
