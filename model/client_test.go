package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/model"
)

// request is what the fake endpoint received.
type request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// fake is an endpoint that records each request and answers with a fixed status and body.
type fake struct {
	srv *httptest.Server

	mu   sync.Mutex
	reqs []request
}

func newFake(t *testing.T, status int, reply string) *fake {
	t.Helper()
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, request{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header, Body: body,
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// client returns a Client for the fake, its Config first passed through edit.
func (f *fake) client(t *testing.T, edit ...func(*model.Config)) *model.Client {
	t.Helper()
	cfg := model.Config{BaseURL: f.srv.URL + "/v1", HTTP: f.srv.Client()}
	for _, e := range edit {
		e(&cfg)
	}
	c, err := model.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// last is the one request the fake received.
func (f *fake) last(t *testing.T) request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 1 {
		t.Fatalf("the endpoint got %d requests, want 1", len(f.reqs))
	}
	return f.reqs[0]
}

// jsonBody decodes a request's JSON body into a generic map.
func jsonBody(t *testing.T, r request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("request body isn't JSON: %v\n%s", err, r.Body)
	}
	return m
}

const chatOK = `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":3,"completion_tokens":1}}`

var hello = model.ChatRequest{
	Model:    "m",
	Messages: []model.Message{{Role: model.RoleUser, Parts: []model.Part{model.Text{Text: "hello"}}}},
}

func TestNewRejectsWiringDefects(t *testing.T) {
	hc := http.DefaultClient
	cases := map[string]model.Config{
		"no HTTP client":  {BaseURL: "http://h/v1"},
		"no base URL":     {HTTP: hc},
		"unparseable":     {BaseURL: "http://h/%zz", HTTP: hc},
		"relative":        {BaseURL: "/v1", HTTP: hc},
		"other scheme":    {BaseURL: "ftp://h/v1", HTTP: hc},
		"query":           {BaseURL: "http://h/v1?api-version=1", HTTP: hc},
		"empty query":     {BaseURL: "http://h/v1?", HTTP: hc},
		"fragment":        {BaseURL: "http://h/v1#x", HTTP: hc},
		"host is missing": {BaseURL: "http:///v1", HTTP: hc},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := model.New(cfg); err == nil || !strings.HasPrefix(err.Error(), "model: ") {
				t.Fatalf("New = %v, want a model: error", err)
			}
		})
	}
}

func TestRouteAndQuery(t *testing.T) {
	for _, base := range []string{"/openai/v1", "/openai/v1/"} {
		t.Run(base, func(t *testing.T) {
			f := newFake(t, http.StatusOK, chatOK)
			q := url.Values{"api-version": {"2025-04-01-preview"}}
			c := f.client(t, func(cfg *model.Config) {
				cfg.BaseURL = f.srv.URL + base
				cfg.Query = q
			})
			// A change to the caller's values after New doesn't reach the requests.
			q.Set("api-version", "changed")
			if _, err := c.Chat(t.Context(), hello); err != nil {
				t.Fatal(err)
			}
			r := f.last(t)
			if r.Method != http.MethodPost || r.Path != "/openai/v1/chat/completions" {
				t.Errorf("request = %s %s, want POST /openai/v1/chat/completions", r.Method, r.Path)
			}
			if got := r.Query.Get("api-version"); got != "2025-04-01-preview" || len(r.Query) != 1 {
				t.Errorf("query = %v, want api-version=2025-04-01-preview", r.Query)
			}
		})
	}
}

func TestAuthorization(t *testing.T) {
	cases := map[string]struct {
		token model.TokenSource
		want  string
	}{
		"token":       {model.StaticToken("secret"), "Bearer secret"},
		"nil source":  {nil, ""},
		"empty token": {model.StaticToken(""), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, http.StatusOK, chatOK)
			c := f.client(t, func(cfg *model.Config) { cfg.Token = tc.token })
			if _, err := c.Chat(t.Context(), hello); err != nil {
				t.Fatal(err)
			}
			h := f.last(t).Header
			if got := h.Get("Authorization"); got != tc.want {
				t.Errorf("Authorization = %q, want %q", got, tc.want)
			}
			if _, ok := h["Authorization"]; !ok && tc.want != "" {
				t.Error("no Authorization header")
			} else if ok && tc.want == "" {
				t.Error("an Authorization header was sent")
			}
		})
	}
}

func TestTokenErrorSendsNothing(t *testing.T) {
	f := newFake(t, http.StatusOK, chatOK)
	boom := errors.New("no credential")
	c := f.client(t, func(cfg *model.Config) {
		cfg.Token = func(context.Context) (string, error) { return "", boom }
	})
	_, err := c.Chat(t.Context(), hello)
	if !errors.Is(err, boom) {
		t.Fatalf("Chat = %v, want the token error", err)
	}
	_, err = c.Transcribe(t.Context(), model.TranscribeRequest{
		Model: "w", Audio: strings.NewReader("RIFF"), Filename: "a.wav",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Transcribe = %v, want the token error", err)
	}
	if n := f.count(); n != 0 {
		t.Fatalf("the endpoint got %d requests, want 0", n)
	}
}

func TestAPIError(t *testing.T) {
	cases := map[string]struct {
		body              string
		message, code     string
		errorTextIncludes string
	}{
		"string code": {
			`{"error":{"message":"bad model","code":"model_not_found"}}`,
			"bad model", "model_not_found", "bad model (model_not_found)",
		},
		"numeric code": {
			`{"error":{"code":400,"message":"too long","type":"invalid_request_error"}}`,
			"too long", "400", "too long (400)",
		},
		"not JSON": {"upstream exploded", "", "", "upstream exploded"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, http.StatusBadRequest, tc.body)
			_, err := f.client(t).Chat(t.Context(), hello)
			var apiErr *model.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("Chat = %v, want an *APIError", err)
			}
			if apiErr.Status != http.StatusBadRequest || apiErr.Message != tc.message ||
				apiErr.Code != tc.code || string(apiErr.Body) != tc.body {
				t.Errorf("APIError = %+v", apiErr)
			}
			for _, want := range []string{"model: chat:", "400", tc.errorTextIncludes} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q doesn't contain %q", err, want)
				}
			}
		})
	}
}

func TestMalformedResponse(t *testing.T) {
	f := newFake(t, http.StatusOK, `{"choices":[`)
	_, err := f.client(t).Chat(t.Context(), hello)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("Chat = %v, want a decode error", err)
	}
}

func TestContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices the client hanging up only once the body has been read.
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	// Cleanups run last first, so the handler is released before Close waits on it.
	t.Cleanup(func() { close(release) })
	c, err := model.New(model.Config{BaseURL: srv.URL + "/v1", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()
	if _, err := c.Chat(ctx, hello); !errors.Is(err, context.Canceled) {
		t.Fatalf("Chat = %v, want context.Canceled", err)
	}
}
