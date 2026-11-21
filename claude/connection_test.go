package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

func TestCancelEndsOnlyToolCalls(t *testing.T) {
	block := harness.Tool{Name: "block", Description: "blocks until cancelled",
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}
	tools, err := mcpbridge.New([]harness.Tool{block}, mcpbridge.Options{Name: serverName})
	if err != nil {
		t.Fatal(err)
	}
	c := newConnection(tools, nil, time.Second)
	if c.tunnel, err = tools.Tunnel(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.tunnel.Close() })
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()

	relay := func(msg string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		out := c.relay(ctx, controlRequest{Subtype: "mcp_message", ServerName: serverName, Message: json.RawMessage(msg)})
		resp, ok := out.(map[string]json.RawMessage)
		if !ok {
			t.Fatalf("relay %s = %v", msg, out)
		}
		return string(resp["mcp_response"])
	}
	relay(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	relay(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if list := relay(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); !strings.Contains(list, `"block"`) {
		t.Fatalf("tools/list after the run ended = %s, want the tools", list)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	c.relay(ctx, controlRequest{Subtype: "mcp_message", ServerName: serverName,
		Message: json.RawMessage(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"block","arguments":{}}}`)})
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a tool call of the ended run took %s", d)
	}
}
