package mcp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"net/http/httptest"
)

// incompleteServer starts a fake MCP server with one mutating tool returning a
// pending-style payload and one read tool returning the same shape.
func incompleteServer(t *testing.T, payload string) (mcpgo.Tool, mcpgo.Tool, *atomic.Pointer[mcpclient.Client], *atomic.Bool, func()) {
	t.Helper()
	srv := mcpserver.NewMCPServer("fake", "1.0.0", mcpserver.WithToolCapabilities(false))
	writeTool := mcpgo.NewTool("update_cells", mcpgo.WithDescription("write"))
	srv.AddTool(writeTool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(payload), nil
	})
	readTool := mcpgo.NewTool("get_sheet_data", mcpgo.WithDescription("read"))
	srv.AddTool(readTool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(payload), nil
	})
	httpSrv := mcpserver.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(httpSrv)

	ss, mcpTools, err := connectAndDiscover(context.Background(), "goclaw", "streamable-http", "", nil, nil, ts.URL, nil, 10)
	if err != nil {
		t.Fatalf("connectAndDiscover: %v", err)
	}
	var writeDef, readDef mcpgo.Tool
	for _, mt := range mcpTools {
		switch mt.Name {
		case "update_cells":
			writeDef = mt
		case "get_sheet_data":
			readDef = mt
		}
	}
	if writeDef.Name == "" || readDef.Name == "" {
		t.Fatal("tools not discovered")
	}
	var ptr atomic.Pointer[mcpclient.Client]
	ptr.Store(ss.client)
	connected := &atomic.Bool{}
	connected.Store(true)
	return writeDef, readDef, &ptr, connected, func() {
		_ = ss.client.Close()
		ts.Close()
	}
}

// TestExecute_IncompleteStatusOnWriteIsError proves fix 4: a mutating tool
// reporting accepted/queued must NOT surface as success.
func TestExecute_IncompleteStatusOnWriteIsError(t *testing.T) {
	for _, payload := range []string{
		`{"status":"accepted","id":"op-1"}`,
		`{"status":"queued"}`,
		`{"state":"pending"}`,
		`{"ok":false,"message":"quota exceeded"}`,
	} {
		writeDef, _, ptr, connected, cleanup := incompleteServer(t, payload)
		bt := NewBridgeTool("fake-sheets", writeDef, ptr, "", 10, connected, uuid.Nil, nil)
		res := bt.Execute(context.Background(), map[string]any{})
		cleanup()
		if !res.IsError {
			t.Errorf("payload %s: mutating tool must return error, got success", payload)
			continue
		}
		if strings.Contains(payload, "accepted") || strings.Contains(payload, "queued") || strings.Contains(payload, "pending") {
			if !strings.Contains(res.ForLLM, "NOT complete") {
				t.Errorf("payload %s: must forbid completion claim:\n%s", payload, res.ForLLM)
			}
		}
	}
}

// TestExecute_IncompleteStatusOnReadPassesThrough proves reads are unaffected:
// a pending *state* is legitimate information for status checks.
func TestExecute_IncompleteStatusOnReadPassesThrough(t *testing.T) {
	_, readDef, ptr, connected, cleanup := incompleteServer(t, `{"status":"pending"}`)
	defer cleanup()
	bt := NewBridgeTool("fake-sheets", readDef, ptr, "", 10, connected, uuid.Nil, nil)
	res := bt.Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("read tool with pending state must pass through, got error: %s", res.ForLLM)
	}
}
