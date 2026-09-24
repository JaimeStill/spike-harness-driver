// Package pi drives the Pi coding agent in RPC mode (pi --mode rpc) as a harness.Driver.
//
// Pi's RPC commands register no tools and load no skills: a tool reaches Pi only through an
// extension, and a skill only through --skill. So the adapter is Go code plus one extension,
// the bridge, which it writes out and loads into every Pi it starts. Discovery stays off, so
// Pi loads the bridge and none of the user's extensions, skills, or context files. The bridge
// registers the session's tools and asks the driver to run each call, and gives each exchange
// with a schema a terminating respond tool whose validated arguments are the structured
// response. Its requests travel as extension UI dialogs on the RPC stream, which the driver
// answers, so a session needs no second transport.
//
// The bridge was checked against Pi 0.87.1. It imports Pi's built-in modules
// (@earendil-works/pi-coding-agent and typebox), whose names have changed before, so a Pi
// upgrade is checked against it.
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
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// toolResult is a tool_execution_end result, as far as the driver reads it: the respond
// tool's details are the structured response.
type toolResult struct {
	Details json.RawMessage `json:"details"`
}

// uiRequest is an extension_ui_request, as far as the driver reads it. It has a line shape of
// its own: a confirm or notify request's message is a string, where an event's is an
// AgentMessage.
type uiRequest struct {
	ID          string `json:"id"`
	Method      string `json:"method"`
	Title       string `json:"title"`
	Placeholder string `json:"placeholder"`
}

// dialog is an extension_ui_request that waits for an answer: a select, confirm, input, or
// editor dialog. The bridge's requests are input dialogs whose placeholder carries the
// request.
type dialog struct {
	Method      string
	Title       string
	Placeholder string
}

// dialogs are the extension UI methods that wait for an answer. The others, such as notify,
// are fire-and-forget.
var dialogs = map[string]bool{"select": true, "confirm": true, "input": true, "editor": true}

// dialogAnswer is an extension_ui_response. Value answers an input dialog; Cancelled dismisses
// any dialog.
type dialogAnswer struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Value     *string `json:"value,omitempty"`
	Cancelled bool    `json:"cancelled,omitempty"`
}

// command is one line written to Pi's stdin. Fields a command doesn't use stay empty.
type command struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`
	Message  string `json:"message,omitempty"`
	Since    string `json:"since,omitempty"`
}

// state is the data of a get_state response, as far as the driver reads it.
type state struct {
	SessionID string `json:"sessionId"`
}

// entries is the data of a get_entries response, as far as the driver reads it: each
// session entry's stable ID, in append order.
type entries struct {
	Entries []struct {
		ID string `json:"id"`
	} `json:"entries"`
}
