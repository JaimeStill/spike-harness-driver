package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxErrorBody = 64 << 10

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

func newAPIError(resp *http.Response) *APIError {
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
