package model_test

import (
	"encoding/base64"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/model"
)

func TestChatWire(t *testing.T) {
	f := newFake(t, http.StatusOK, chatOK)
	png := []byte{0x89, 'P', 'N', 'G'}
	wav := []byte("RIFF....WAVE")
	_, err := f.client(t).Chat(t.Context(), model.ChatRequest{
		Model: "gpt",
		Messages: []model.Message{
			{Role: model.RoleSystem, Parts: []model.Part{model.Text{Text: "be brief"}}},
			{Role: model.RoleUser, Parts: []model.Part{
				model.Text{Text: "what is this?"},
				model.Image{MediaType: "image/png", Data: png},
				model.Audio{Format: "wav", Data: wav},
			}},
			{Role: model.RoleAssistant, Parts: []model.Part{model.Text{Text: "a cat"}}},
		},
		MaxCompletionTokens: 64,
		ReasoningEffort:     "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	want := map[string]any{
		"model": "gpt",
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "what is this?"},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
				}},
				map[string]any{"type": "input_audio", "input_audio": map[string]any{
					"data": base64.StdEncoding.EncodeToString(wav), "format": "wav",
				}},
			}},
			map[string]any{"role": "assistant", "content": "a cat"},
		},
		"max_completion_tokens": float64(64),
		"reasoning_effort":      "low",
	}
	if got := jsonBody(t, r); !reflect.DeepEqual(got, want) {
		t.Errorf("body =\n%s\nwant\n%v", r.Body, want)
	}
}

func TestChatOmitsUnsetOptions(t *testing.T) {
	f := newFake(t, http.StatusOK, chatOK)
	if _, err := f.client(t).Chat(t.Context(), hello); err != nil {
		t.Fatal(err)
	}
	body := jsonBody(t, f.last(t))
	for _, key := range []string{
		"max_completion_tokens", "reasoning_effort", "max_tokens", "temperature", "stream",
	} {
		if _, ok := body[key]; ok {
			t.Errorf("body has %q: %v", key, body)
		}
	}
}

func TestChatResponse(t *testing.T) {
	cases := map[string]struct {
		reply string
		want  model.ChatResponse
	}{
		"text": {chatOK, model.ChatResponse{
			Text: "hi", FinishReason: "stop", Usage: model.Usage{InputTokens: 3, OutputTokens: 1},
		}},
		"null content": {
			`{"choices":[{"message":{"content":null},"finish_reason":"length"}]}`,
			model.ChatResponse{FinishReason: "length"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, http.StatusOK, tc.reply)
			got, err := f.client(t).Chat(t.Context(), hello)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Chat = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestChatNoChoices(t *testing.T) {
	f := newFake(t, http.StatusOK, `{"choices":[]}`)
	if _, err := f.client(t).Chat(t.Context(), hello); err == nil ||
		!strings.Contains(err.Error(), "no choices") {
		t.Fatalf("Chat = %v, want a no-choices error", err)
	}
}

// embedded satisfies Part by embedding Text, but is none of the parts the package knows.
type embedded struct{ model.Text }

func TestChatValidation(t *testing.T) {
	user := func(parts ...model.Part) []model.Message {
		return []model.Message{{Role: model.RoleUser, Parts: parts}}
	}
	cases := map[string]model.ChatRequest{
		"no model":      {Messages: hello.Messages},
		"no messages":   {Model: "m"},
		"no role":       {Model: "m", Messages: []model.Message{{Parts: []model.Part{model.Text{}}}}},
		"no parts":      {Model: "m", Messages: user()},
		"nil part":      {Model: "m", Messages: user(model.Text{}, nil)},
		"unknown part":  {Model: "m", Messages: user(embedded{})},
		"image no type": {Model: "m", Messages: user(model.Image{Data: []byte{1}})},
		"image no data": {Model: "m", Messages: user(model.Image{MediaType: "image/png"})},
		"audio no fmt":  {Model: "m", Messages: user(model.Audio{Data: []byte{1}})},
		"audio no data": {Model: "m", Messages: user(model.Audio{Format: "wav"})},
	}
	f := newFake(t, http.StatusOK, chatOK)
	c := f.client(t)
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Chat(t.Context(), req); err == nil ||
				!strings.HasPrefix(err.Error(), "model: chat: ") {
				t.Fatalf("Chat = %v, want a model: chat: error", err)
			}
		})
	}
	if n := f.count(); n != 0 {
		t.Fatalf("the endpoint got %d requests, want 0", n)
	}
}
