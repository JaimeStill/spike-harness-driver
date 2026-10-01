package opencode

import "encoding/json"

// The wire types of the Agent Client Protocol as OpenCode speaks it: JSON-RPC 2.0, one message
// per line, at protocol version 1. Only the fields the driver reads or writes are named.

// call is a request or notification the driver sends. Encode adds the id, or none for a
// notification.
type call struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

// rpcMessage is any JSON-RPC message, read far enough to tell a response from a request or a
// notification.
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// request is a request OpenCode makes of the driver, such as session/request_permission.
type request struct {
	Method string
	Params json.RawMessage
}

// contentBlock is one block of a prompt or of an update's content.
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// mcpServer names an MCP server for a session: the driver's own, over HTTP.
type mcpServer struct {
	Type    string       `json:"type"`
	Name    string       `json:"name"`
	URL     string       `json:"url"`
	Headers []httpHeader `json:"headers"`
}

type httpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// sessionUpdate is a session/update notification's update.
type sessionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	// Content is a message chunk's content block, or a tool call's list of content.
	Content   json.RawMessage `json:"content,omitempty"`
	MessageID string          `json:"messageId,omitempty"`
	// ToolCallID, Title, Kind, Status, RawInput, and RawOutput are a tool call's.
	ToolCallID string          `json:"toolCallId,omitempty"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage `json:"rawOutput,omitempty"`
}

// promptResult is session/prompt's result, which arrives as the turn ends.
type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		Input      int `json:"inputTokens"`
		Output     int `json:"outputTokens"`
		CacheRead  int `json:"cachedReadTokens"`
		CacheWrite int `json:"cachedWriteTokens"`
	} `json:"usage"`
}
