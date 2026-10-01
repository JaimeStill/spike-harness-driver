package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxErrorBody bounds how much of an error response is read. It is a safety bound against an
// endpoint that streams an unbounded error body, not a tuning choice: an error message fits
// many times over.
const maxErrorBody = 64 << 10

// maxErrorText bounds how much of an unparsed error body Error prints.
const maxErrorText = 512

// APIError is a non-2xx response from the endpoint. Requests return it wrapped, so callers
// reach it with errors.As.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Message and Code are the error object's message and code, when the body is an
	// OpenAI-style {"error": {...}}. A numeric code is kept as its decimal text.
	Message string
	Code    string
	// Body is the response body, up to 64 KiB.
	Body []byte
}

// Error reports the status and the error's message, or the start of the body when it has no
// message.
func (e *APIError) Error() string {
	s := fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status))
	switch {
	case e.Message != "" && e.Code != "":
		return fmt.Sprintf("%s: %s (%s)", s, e.Message, e.Code)
	case e.Message != "":
		return s + ": " + e.Message
	}
	body := bytes.TrimSpace(e.Body)
	if len(body) == 0 {
		return s
	}
	if len(body) > maxErrorText {
		return fmt.Sprintf("%s: %s...", s, body[:maxErrorText])
	}
	return fmt.Sprintf("%s: %s", s, body)
}

// newAPIError reads resp's body, up to maxErrorBody, into an APIError.
func newAPIError(resp *http.Response) *APIError {
	// A failed read leaves the part of the body that was read, which is still the best
	// account of the error there is.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	e := &APIError{Status: resp.StatusCode, Body: body}
	var wire struct {
		Error struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wire) == nil {
		e.Message = wire.Error.Message
		e.Code = code(wire.Error.Code)
	}
	return e
}

// code is an error code as text. Endpoints differ in sending a string or a number.
func code(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}
