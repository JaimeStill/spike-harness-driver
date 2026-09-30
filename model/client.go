// Package model is a direct client for OpenAI-compatible model endpoints: chat, including
// image and audio parts, embeddings, and transcription. It sits beside harness as the second
// way a Go program reaches a model. Where a harness runs an agent loop, a Client makes one
// request and returns its answer.
//
// A Client serves one endpoint, which Config describes: the base URL every route is appended
// to, a query every request carries, and the token a request authenticates with. An endpoint
// that splits its routes across base URLs, or needs a different query for one route, takes a
// Client per base URL. The caller supplies the http.Client, and with it every timeout and
// retry, because the package sets no policy of its own.
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

// TokenSource returns the bearer token for one request. It is called for every request, so a
// source whose tokens expire can refresh them. An empty token sends no Authorization header.
type TokenSource func(ctx context.Context) (string, error)

// StaticToken returns a TokenSource that always returns token. An empty token sends no
// Authorization header, which suits an endpoint without authentication.
func StaticToken(token string) TokenSource {
	return func(context.Context) (string, error) { return token, nil }
}

// Config describes the endpoint a Client talks to.
type Config struct {
	// BaseURL is the endpoint's API root, such as "http://localhost:8080/v1". Each route, such
	// as "chat/completions", is appended to its path. It carries no query or fragment; Query
	// holds the query.
	BaseURL string
	// Query is added to every request's URL, as an endpoint that versions its API by a query
	// parameter requires.
	Query url.Values
	// Token authenticates each request as a bearer token. Nil sends no Authorization header.
	Token TokenSource
	// HTTP sends the requests. It is required, and its timeouts and transport are the
	// Client's.
	HTTP *http.Client
}

// Client makes requests of one OpenAI-compatible endpoint. It is safe for concurrent use.
type Client struct {
	base  url.URL
	query string
	token TokenSource
	http  *http.Client
}

// New returns a Client for cfg. It makes no request, so it reports only a Config that can't
// describe an endpoint: no HTTP client, or a BaseURL that isn't an absolute http or https URL
// without a query or fragment.
func New(cfg Config) (*Client, error) {
	if cfg.HTTP == nil {
		return nil, errors.New("model: config has no HTTP client")
	}
	if cfg.BaseURL == "" {
		return nil, errors.New("model: config has no base URL")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("model: base URL: %w", err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https", u.Host == "":
		return nil, fmt.Errorf("model: base URL %q isn't an absolute http or https URL", cfg.BaseURL)
	case u.RawQuery != "" || u.ForceQuery:
		return nil, fmt.Errorf("model: base URL %q has a query; set Config.Query instead", cfg.BaseURL)
	case u.Fragment != "":
		return nil, fmt.Errorf("model: base URL %q has a fragment", cfg.BaseURL)
	}
	// The query is encoded once, from a copy, so a caller changing its url.Values after New
	// can't change the requests.
	return &Client{
		base:  *u,
		query: url.Values(maps.Clone(cfg.Query)).Encode(),
		token: cfg.Token,
		http:  cfg.HTTP,
	}, nil
}

// endpoint is the URL of route, appended to the base path with exactly one slash between them.
func (c *Client) endpoint(route string) string {
	u := c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + route
	u.RawPath = ""
	u.RawQuery = c.query
	return u.String()
}

// postJSON sends in as the JSON body of a POST to route and decodes the response into out.
func (c *Client) postJSON(ctx context.Context, op, route string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("model: %s: encode request: %w", op, err)
	}
	return c.post(ctx, op, route, "application/json", bytes.NewReader(body), out)
}

// post sends body as a POST to route and decodes the JSON response into out. A non-2xx
// response is an *APIError. post closes body when it is an io.Closer and no request takes it,
// so a writer feeding it through a pipe is released.
func (c *Client) post(
	ctx context.Context, op, route, contentType string, body io.Reader, out any,
) error {
	token := ""
	if c.token != nil {
		t, err := c.token(ctx)
		if err != nil {
			closeBody(body)
			return fmt.Errorf("model: %s: token: %w", op, err)
		}
		token = t
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(route), body)
	if err != nil {
		closeBody(body)
		return fmt.Errorf("model: %s: %w", op, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("model: %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("model: %s: %w", op, newAPIError(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("model: %s: decode response: %w", op, err)
	}
	return nil
}

func closeBody(body io.Reader) {
	if c, ok := body.(io.Closer); ok {
		_ = c.Close()
	}
}

// Usage counts the tokens a request used, as the endpoint reports them.
type Usage struct {
	// InputTokens counts the prompt's tokens, or an embedding request's input.
	InputTokens int
	// OutputTokens counts the tokens generated. An embedding request generates none.
	OutputTokens int
}

// wireUsage is the usage object the endpoints return.
type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (u wireUsage) usage() Usage {
	return Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
}
