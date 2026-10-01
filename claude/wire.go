package claude

import "encoding/json"

// The wire types of Claude Code's stream-json protocol, as far as the driver reads and writes
// it. Every line is one JSON object whose type field says what it is. The driver writes user
// messages and control requests, and reads everything else.

// line is the head every stdout line shares.
type line struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

// userMessage is a user turn the driver sends. Claude Code answers it with no response of its
// own: the turn's outcome arrives as events, ending with a result.
type userMessage struct {
	Type            string      `json:"type"`
	Message         userContent `json:"message"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
	SessionID       string      `json:"session_id"`
}

type userContent struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock is one block of a message: text, an image, a tool call, or a tool's result.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Source is an image's.
	Source *imageSource `json:"source,omitempty"`
	// ID, Name, and Input are a tool_use block's.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// ToolUseID, Content, and IsError are a tool_result block's. Content is a string or a list
	// of blocks.
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// controlRequest is a request in either direction: the driver's of Claude Code, such as
// initialize or interrupt, or Claude Code's of the driver, such as mcp_message or
// can_use_tool. Only the fields of the subtypes the driver uses are named.
type controlRequest struct {
	Subtype string `json:"subtype"`

	// Hooks is initialize's; null registers none.
	Hooks *struct{} `json:"hooks"`

	// ServerName and Message are mcp_message's: one JSON-RPC message for the named SDK MCP
	// server.
	ServerName string          `json:"server_name,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`

	// ToolName and Input are can_use_tool's.
	ToolName string          `json:"tool_name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// controlEnvelope carries a control request on the wire.
type controlEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// controlResponse answers a control request, in either direction.
type controlResponse struct {
	Type     string       `json:"type"`
	Response controlReply `json:"response"`
}

type controlReply struct {
	Subtype   string          `json:"subtype"` // success or error
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// streamEvent wraps one raw Messages API stream event, sent with --include-partial-messages.
type streamEvent struct {
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
}

// message is an assistant or user message: the assistant's tool calls, and the tool results
// Claude Code sends back as the user.
type message struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// result ends a turn.
type result struct {
	Subtype        string   `json:"subtype"`
	IsError        bool     `json:"is_error"`
	Result         string   `json:"result"`
	StopReason     *string  `json:"stop_reason"`
	TerminalReason string   `json:"terminal_reason"`
	Errors         []string `json:"errors"`
	APIErrorStatus *int     `json:"api_error_status"`
	Usage          *usage   `json:"usage"`
}

// usage is a result's, summed over the turn's model requests.
type usage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

// rateLimit is a rate_limit_event's info: where the subscription stands against its limits.
type rateLimit struct {
	Info struct {
		Status         string                 `json:"status"`
		ResetsAt       int64                  `json:"resetsAt"`
		RateLimitType  string                 `json:"rateLimitType"`
		UnifiedWindows map[string]limitWindow `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

type limitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}
