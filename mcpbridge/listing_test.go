package mcpbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A harness's first listing may still be in flight when respond changes, as OpenCode's is at
// session/new. That listing started before the change and may answer with the old tools, so
// WaitListed waits for one that started after it. The test drives routeRespond directly, so it
// can hold a listing in flight.
func TestWaitListedWaitsOutAListingFromBeforeTheChange(t *testing.T) {
	s, err := New(nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	held := s.routeRespond(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		close(entered)
		<-release
		return &mcp.ListToolsResult{}, nil
	})
	current := s.routeRespond(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{}, nil
	})
	first := make(chan struct{})
	go func() {
		defer close(first)
		_, _ = held(t.Context(), "tools/list", &mcp.ListToolsRequest{})
	}()
	<-entered

	if err := s.SetSchema(json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- s.WaitListed(t.Context(), 5*time.Second) }()
	select {
	case err := <-waited:
		t.Fatalf("WaitListed = %v while the only listing predates the change", err)
	case <-time.After(100 * time.Millisecond):
	}

	// The listing from before the change answers; it doesn't count.
	close(release)
	<-first
	select {
	case err := <-waited:
		t.Fatalf("WaitListed = %v after a listing that started before the change", err)
	case <-time.After(100 * time.Millisecond):
	}

	// A listing that starts after the change ends the wait.
	if _, err := current(t.Context(), "tools/list", &mcp.ListToolsRequest{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitListed went on after a current listing")
	}
}
