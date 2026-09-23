// Package pi drives the Pi coding agent in RPC mode (pi --mode rpc) as a harness.Driver.
package pi

import "encoding/json"

// record is one line read from Pi's stdout: a response to a command when Type is "response",
// and an event otherwise.
type record struct {
	Type string `json:"type"`

	// Response fields. ID echoes the command's id.
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`

	// message_update carries a delta.
	AssistantMessageEvent *assistantEvent `json:"assistantMessageEvent"`
	// message_start, message_end, and turn_end carry a message.
	Message *message `json:"message"`

	// tool_execution_* fields.
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"isError"`
}

type assistantEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

// message is an AgentMessage. Content is a string for some roles and a block list for
// assistant messages, so it stays raw until an assistant message needs its text.
type message struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        *usage          `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// command is one line written to Pi's stdin. Fields a command doesn't use stay empty.
type command struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`
	Message  string `json:"message,omitempty"`
}

// state is the data of a get_state response, as far as the driver reads it.
type state struct {
	SessionID string `json:"sessionId"`
}
