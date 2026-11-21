package opencode

import "encoding/json"

type call struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

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

type request struct {
	Method string
	Params json.RawMessage
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

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

type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content,omitempty"`
	MessageID     string          `json:"messageId,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		Input      int `json:"inputTokens"`
		Output     int `json:"outputTokens"`
		CacheRead  int `json:"cachedReadTokens"`
		CacheWrite int `json:"cachedWriteTokens"`
	} `json:"usage"`
}
