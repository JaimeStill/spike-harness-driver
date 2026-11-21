package claude

import "encoding/json"

type line struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

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

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type controlRequest struct {
	Subtype string `json:"subtype"`

	Hooks *struct{} `json:"hooks"`

	ServerName string          `json:"server_name,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`

	ToolName string          `json:"tool_name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type controlEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

type controlResponse struct {
	Type     string       `json:"type"`
	Response controlReply `json:"response"`
}

type controlReply struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}

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

type message struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

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

type usage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

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
