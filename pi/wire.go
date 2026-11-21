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
//
// See docs/design.md#the-pi-adapter
package pi

import "encoding/json"

type record struct {
	Type string `json:"type"`

	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`

	AssistantMessageEvent *assistantEvent `json:"assistantMessageEvent"`
	Message               *message        `json:"message"`

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

type toolResult struct {
	Content json.RawMessage `json:"content"`
	Details json.RawMessage `json:"details"`
}

type uiRequest struct {
	ID          string `json:"id"`
	Method      string `json:"method"`
	Title       string `json:"title"`
	Placeholder string `json:"placeholder"`
}

type dialog struct {
	Method      string
	Title       string
	Placeholder string
}

var dialogs = map[string]bool{"select": true, "confirm": true, "input": true, "editor": true}

type dialogAnswer struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Value     *string `json:"value,omitempty"`
	Cancelled bool    `json:"cancelled,omitempty"`
}

type command struct {
	ID       string  `json:"id,omitempty"`
	Type     string  `json:"type"`
	Provider string  `json:"provider,omitempty"`
	ModelID  string  `json:"modelId,omitempty"`
	Message  string  `json:"message,omitempty"`
	Images   []image `json:"images,omitempty"`
	Since    string  `json:"since,omitempty"`
}

type image struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type state struct {
	SessionID string `json:"sessionId"`
}

type entries struct {
	Entries []struct {
		ID string `json:"id"`
	} `json:"entries"`
}
