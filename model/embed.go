package model

import (
	"context"
	"errors"
	"fmt"
)

// EmbedRequest asks for one embedding per input.
type EmbedRequest struct {
	Model string
	Input []string
	// Dimensions asks a model that can shorten its embeddings for that many. Zero takes the
	// model's own.
	Dimensions int
}

// EmbedResponse holds the embeddings, Vectors[i] being Input[i]'s.
type EmbedResponse struct {
	Vectors [][]float32
	Usage   Usage
}

type embedBody struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format"`
	Dimensions     int      `json:"dimensions,omitempty"`
}

type embedReply struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage wireUsage `json:"usage"`
}

// Embed returns an embedding for each of req's inputs. The request is checked before it is
// sent, and the response is checked to hold exactly one embedding per input.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	switch {
	case req.Model == "":
		return EmbedResponse{}, errors.New("model: embed: the request names no model")
	case len(req.Input) == 0:
		return EmbedResponse{}, errors.New("model: embed: the request has no input")
	}
	body := embedBody{
		Model: req.Model, Input: req.Input, EncodingFormat: "float", Dimensions: req.Dimensions,
	}
	var reply embedReply
	if err := c.postJSON(ctx, "embed", "embeddings", body, &reply); err != nil {
		return EmbedResponse{}, err
	}
	if len(reply.Data) != len(req.Input) {
		return EmbedResponse{}, fmt.Errorf("model: embed: %d embeddings for %d inputs",
			len(reply.Data), len(req.Input))
	}
	// The data carries its input's index, and an endpoint that batches may return it out of
	// order.
	vectors := make([][]float32, len(req.Input))
	for _, d := range reply.Data {
		switch {
		case d.Index < 0 || d.Index >= len(vectors):
			return EmbedResponse{}, fmt.Errorf("model: embed: index %d is out of range", d.Index)
		case vectors[d.Index] != nil:
			return EmbedResponse{}, fmt.Errorf("model: embed: index %d repeats", d.Index)
		case d.Embedding == nil:
			return EmbedResponse{}, fmt.Errorf("model: embed: index %d has no embedding", d.Index)
		}
		vectors[d.Index] = d.Embedding
	}
	return EmbedResponse{Vectors: vectors, Usage: reply.Usage.usage()}, nil
}
