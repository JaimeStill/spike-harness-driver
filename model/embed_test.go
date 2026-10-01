package model_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/model"
)

func TestEmbedWire(t *testing.T) {
	const reply = `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[2]}]}`
	cases := map[string]struct {
		dimensions int
		want       map[string]any
	}{
		"model's dimensions": {0, map[string]any{
			"model": "e", "input": []any{"a", "b"}, "encoding_format": "float",
		}},
		"chosen dimensions": {256, map[string]any{
			"model": "e", "input": []any{"a", "b"}, "encoding_format": "float",
			"dimensions": float64(256),
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, http.StatusOK, reply)
			_, err := f.client(t).Embed(t.Context(), model.EmbedRequest{
				Model: "e", Input: []string{"a", "b"}, Dimensions: tc.dimensions,
			})
			if err != nil {
				t.Fatal(err)
			}
			r := f.last(t)
			if r.Path != "/v1/embeddings" {
				t.Errorf("path = %s", r.Path)
			}
			if got := jsonBody(t, r); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("body = %s, want %v", r.Body, tc.want)
			}
		})
	}
}

func TestEmbedOrdersByIndex(t *testing.T) {
	f := newFake(t, http.StatusOK, `{"data":[
		{"index":2,"embedding":[3,3]},{"index":0,"embedding":[1,1]},{"index":1,"embedding":[2,2]}
	],"usage":{"prompt_tokens":6,"total_tokens":6}}`)
	got, err := f.client(t).Embed(t.Context(), model.EmbedRequest{
		Model: "e", Input: []string{"a", "b", "c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := model.EmbedResponse{
		Vectors: [][]float32{{1, 1}, {2, 2}, {3, 3}},
		Usage:   model.Usage{InputTokens: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Embed = %+v, want %+v", got, want)
	}
}

func TestEmbedRejectsMismatchedData(t *testing.T) {
	cases := map[string]string{
		"too few": `{"data":[{"index":0,"embedding":[1]}]}`,
		"too many": `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[1]},
			{"index":2,"embedding":[1]}]}`,
		"out of range": `{"data":[{"index":0,"embedding":[1]},{"index":2,"embedding":[1]}]}`,
		"negative":     `{"data":[{"index":0,"embedding":[1]},{"index":-1,"embedding":[1]}]}`,
		"repeated":     `{"data":[{"index":1,"embedding":[1]},{"index":1,"embedding":[1]}]}`,
		"no embedding": `{"data":[{"index":0,"embedding":[1]},{"index":1}]}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, http.StatusOK, reply)
			_, err := f.client(t).Embed(t.Context(), model.EmbedRequest{
				Model: "e", Input: []string{"a", "b"},
			})
			if err == nil || !strings.HasPrefix(err.Error(), "model: embed: ") {
				t.Fatalf("Embed = %v, want a model: embed: error", err)
			}
		})
	}
}

func TestEmbedValidation(t *testing.T) {
	f := newFake(t, http.StatusOK, `{"data":[]}`)
	c := f.client(t)
	for name, req := range map[string]model.EmbedRequest{
		"no model": {Input: []string{"a"}},
		"no input": {Model: "e"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Embed(t.Context(), req); err == nil {
				t.Fatal("Embed succeeded, want an error")
			}
		})
	}
	if n := f.count(); n != 0 {
		t.Fatalf("the endpoint got %d requests, want 0", n)
	}
}
