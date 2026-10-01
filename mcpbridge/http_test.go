package mcpbridge_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// bearer adds an Authorization header to every request it carries.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func listen(t *testing.T) (*mcpbridge.Server, *mcpbridge.Endpoint) {
	t.Helper()
	s, err := mcpbridge.New(testTools(), mcpbridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Listen(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return s, e
}

func TestListenRequiresToken(t *testing.T) {
	_, e := listen(t)
	if !strings.HasPrefix(e.URL, "http://127.0.0.1:") {
		t.Errorf("URL = %q, want a loopback address", e.URL)
	}
	if len(e.Token) != 64 {
		t.Errorf("token %q isn't 32 hex-encoded bytes", e.Token)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	for name, auth := range map[string]string{
		"none":      "",
		"wrong":     "Bearer " + strings.Repeat("0", 64),
		"truncated": "Bearer " + e.Token[:63],
		"no scheme": e.Token,
	} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.URL, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestListenClient runs go-sdk's streamable client against the endpoint, and checks that the
// client learns of a tool list change on its standalone stream.
func TestListenClient(t *testing.T) {
	s, e := listen(t)
	changed := make(chan struct{}, 8)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
	})
	transport := &mcp.StreamableClientTransport{
		Endpoint:   e.URL,
		HTTPClient: &http.Client{Transport: bearer{e.Token, http.DefaultTransport}},
		MaxRetries: -1,
	}
	cs, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	exerciseClient(t, s, cs, changed)

	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(t.Context(), nil); err == nil {
		t.Error("ListTools succeeded after the endpoint closed")
	}
}
