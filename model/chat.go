package model

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
)

// Role is who a chat message is from.
type Role string

// The roles a chat message takes.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one chat message: a role and its content, in parts.
type Message struct {
	Role  Role
	Parts []Part
}

// Part is one piece of a message's content: a Text, an Image, or an Audio. The set is closed,
// because each part has its own wire shape.
type Part interface{ isPart() }

// Text is a part of plain text.
type Text struct{ Text string }

// Image is a part holding an image, sent inline as a data URI.
type Image struct {
	// MediaType is the image's media type, such as "image/png".
	MediaType string
	Data      []byte
}

// Audio is a part holding an audio clip, sent inline for a model that takes audio input.
type Audio struct {
	// Format is the clip's format, such as "wav" or "mp3".
	Format string
	Data   []byte
}

func (Text) isPart()  {}
func (Image) isPart() {}
func (Audio) isPart() {}

// ChatRequest is one chat completion request.
type ChatRequest struct {
	Model    string
	Messages []Message
	// MaxCompletionTokens caps the tokens generated, reasoning included. Zero leaves the cap
	// to the endpoint.
	MaxCompletionTokens int
	// ReasoningEffort, such as "low" or "high", is passed to a reasoning model. Empty leaves
	// it to the endpoint.
	ReasoningEffort string
}

// ChatResponse is the first choice of a chat completion.
type ChatResponse struct {
	// Text is the message's content, empty when the endpoint returns none.
	Text string
	// FinishReason is why generation stopped, such as "stop" or "length".
	FinishReason string
	Usage        Usage
}

// chatBody is the request body. It never carries max_tokens or temperature, which reasoning
// models reject, nor stream, which this client doesn't read.
type chatBody struct {
	Model               string        `json:"model"`
	Messages            []wireMessage `json:"messages"`
	MaxCompletionTokens int           `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string        `json:"reasoning_effort,omitempty"`
}

// wireMessage is a message on the wire. Its content is a string for a message that is a
// single Text, the form every endpoint and chat template takes, and an array of parts
// otherwise.
type wireMessage struct {
	Role    Role `json:"role"`
	Content any  `json:"content"`
}

type wireText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireImage struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

type wireAudio struct {
	Type       string `json:"type"`
	InputAudio struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio"`
}

type chatReply struct {
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage wireUsage `json:"usage"`
}

// Chat sends req and returns the first choice. The request is checked before it is sent.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := chatWire(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("model: chat: %w", err)
	}
	var reply chatReply
	if err := c.postJSON(ctx, "chat", "chat/completions", body, &reply); err != nil {
		return ChatResponse{}, err
	}
	if len(reply.Choices) == 0 {
		return ChatResponse{}, errors.New("model: chat: the response has no choices")
	}
	ch := reply.Choices[0]
	resp := ChatResponse{FinishReason: ch.FinishReason, Usage: reply.Usage.usage()}
	if ch.Message.Content != nil {
		resp.Text = *ch.Message.Content
	}
	return resp, nil
}

// chatWire checks req and builds its request body.
func chatWire(req ChatRequest) (chatBody, error) {
	if req.Model == "" {
		return chatBody{}, errors.New("the request names no model")
	}
	if len(req.Messages) == 0 {
		return chatBody{}, errors.New("the request has no messages")
	}
	body := chatBody{
		Model:               req.Model,
		Messages:            make([]wireMessage, len(req.Messages)),
		MaxCompletionTokens: req.MaxCompletionTokens,
		ReasoningEffort:     req.ReasoningEffort,
	}
	for i, m := range req.Messages {
		if m.Role == "" {
			return chatBody{}, fmt.Errorf("message %d has no role", i)
		}
		content, err := contentWire(m.Parts)
		if err != nil {
			return chatBody{}, fmt.Errorf("message %d: %w", i, err)
		}
		body.Messages[i] = wireMessage{Role: m.Role, Content: content}
	}
	return body, nil
}

// contentWire is a message's content on the wire.
func contentWire(parts []Part) (any, error) {
	if len(parts) == 0 {
		return nil, errors.New("no parts")
	}
	if t, ok := parts[0].(Text); ok && len(parts) == 1 {
		return t.Text, nil
	}
	wire := make([]any, len(parts))
	for i, p := range parts {
		switch p := p.(type) {
		case Text:
			wire[i] = wireText{Type: "text", Text: p.Text}
		case Image:
			if p.MediaType == "" || len(p.Data) == 0 {
				return nil, fmt.Errorf("part %d: an image needs a media type and data", i)
			}
			w := wireImage{Type: "image_url"}
			w.ImageURL.URL = "data:" + p.MediaType + ";base64," +
				base64.StdEncoding.EncodeToString(p.Data)
			wire[i] = w
		case Audio:
			if p.Format == "" || len(p.Data) == 0 {
				return nil, fmt.Errorf("part %d: audio needs a format and data", i)
			}
			w := wireAudio{Type: "input_audio"}
			w.InputAudio.Data = base64.StdEncoding.EncodeToString(p.Data)
			w.InputAudio.Format = p.Format
			wire[i] = w
		default:
			return nil, fmt.Errorf("part %d: unknown part type %T", i, p)
		}
	}
	return wire, nil
}
