package stdio

import (
	"encoding/json"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Codec translates between a harness's line protocol and the harness package. It is where an
// adapter's normalization lives.
type Codec interface {
	// Encode renders cmd as one line, stamped with the correlation id the harness will echo
	// in its response.
	Encode(id string, cmd any) ([]byte, error)
	// Decode parses one line into a response or the normalized events it carries. A line that
	// carries no normalized meaning still yields an EventHarness, so nothing is lost.
	Decode(line []byte) (Frame, error)
}

// Frame is one decoded line: a response to a command, or events.
type Frame struct {
	Response *Response
	Events   []harness.Event
}

// Response answers one command.
type Response struct {
	// ID is the correlation id the command was encoded with.
	ID string
	// Err is non-nil when the harness reported that the command failed.
	Err  error
	Data json.RawMessage
}
