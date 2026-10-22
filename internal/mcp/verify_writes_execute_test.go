package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// TestExecute_AutoVerifyWrite proves the full write→re-read path: a mutating
// tool that succeeds gets its configured read tool called automatically and
// the read-back data appended to the result the model sees.
func TestExecute_AutoVerifyWrite(t *testing.T) {
	stored := "empty"
	srv := mcpserver.NewMCPServer("fake-sheets", "1.0.0", mcpserver.WithToolCapabilities(false))

	writeTool := mcpgo.NewTool("update_cells",
		mcpgo.WithDescription("write cells"),
		mcpgo.WithString("spreadsheet_id", mcpgo.Required()),
		mcpgo.WithString("sheet", mcpgo.Required()),
		mcpgo.WithString("range", mcpgo.Required()),
	)
	srv.AddTool(writeTool, func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		stored = "debt-row-387k"
		return mcpgo.NewToolResultText("updated 1 cell"), nil
	})
	readTool := mcpgo.NewTool("get_sheet_data",
		mcpgo.WithDescription("read cells"),
		mcpgo.WithString("spreadsheet_id", mcpgo.Required()),
	)
	srv.AddTool(readTool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("stored=" + stored), nil
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(httpSrv)
	defer ts.Close()

	ss, mcpTools, err := connectAndDiscover(context.Background(), "goclaw", "streamable-http", "", nil, nil, ts.URL, nil, 10)
	if err != nil {
		t.Fatalf("connectAndDiscover: %v", err)
	}
	defer func() { _ = ss.client.Close() }()

	var writeDef mcpgo.Tool
	for _, mt := range mcpTools {
		if mt.Name == "update_cells" {
			writeDef = mt
		}
	}
	if writeDef.Name == "" {
		t.Fatal("update_cells not discovered")
	}

	var ptr atomic.Pointer[mcpclient.Client]
	ptr.Store(ss.client)
	connected := &atomic.Bool{}
	connected.Store(true)
	bt := NewBridgeTool("fake-sheets", writeDef, &ptr, "", 10, connected, uuid.Nil, nil).
		WithVerifyWrites(map[string]VerifySpec{
			"update_cells": {
				Tool: "get_sheet_data",
				Args: map[string]string{
					"spreadsheet_id": "$spreadsheet_id",
					"sheet":          "$sheet",
				},
				MaxChars: 2000,
			},
		})

	res := bt.Execute(context.Background(), map[string]any{
		"spreadsheet_id": "sid-1", "sheet": "Sheet1", "range": "A1:A1",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "[Auto-verify]") {
		t.Errorf("expected auto-verify section in result:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "stored=debt-row-387k") {
		t.Errorf("expected re-read data in result:\n%s", res.ForLLM)
	}
}

// TestExecute_AutoVerifyReadFailure proves the honest path: when the write
// succeeds but the confirming re-read fails, the result says so explicitly
// instead of letting the model claim silent success.
func TestExecute_AutoVerifyReadFailure(t *testing.T) {
	srv := mcpserver.NewMCPServer("fake-sheets", "1.0.0", mcpserver.WithToolCapabilities(false))

	writeTool := mcpgo.NewTool("append_rows",
		mcpgo.WithDescription("append"),
		mcpgo.WithString("spreadsheet_id", mcpgo.Required()),
	)
	srv.AddTool(writeTool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("appended 1 row"), nil
	})
	badRead := mcpgo.NewTool("get_sheet_data",
		mcpgo.WithDescription("read"),
		mcpgo.WithString("spreadsheet_id", mcpgo.Required()),
	)
	srv.AddTool(badRead, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultError("boom: drive unreachable"), nil
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(httpSrv)
	defer ts.Close()

	ss, mcpTools, err := connectAndDiscover(context.Background(), "goclaw", "streamable-http", "", nil, nil, ts.URL, nil, 10)
	if err != nil {
		t.Fatalf("connectAndDiscover: %v", err)
	}
	defer func() { _ = ss.client.Close() }()

	var writeDef mcpgo.Tool
	for _, mt := range mcpTools {
		if mt.Name == "append_rows" {
			writeDef = mt
		}
	}
	if writeDef.Name == "" {
		t.Fatal("append_rows not discovered")
	}

	var ptr atomic.Pointer[mcpclient.Client]
	ptr.Store(ss.client)
	connected := &atomic.Bool{}
	connected.Store(true)
	bt := NewBridgeTool("fake-sheets", writeDef, &ptr, "", 10, connected, uuid.Nil, nil).
		WithVerifyWrites(map[string]VerifySpec{
			"append_rows": {Tool: "get_sheet_data", Args: map[string]string{"spreadsheet_id": "$spreadsheet_id"}},
		})

	res := bt.Execute(context.Background(), map[string]any{"spreadsheet_id": "sid-1"})
	if res.IsError {
		t.Fatalf("write itself succeeded; result must not be an error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Auto-verify FAILED") {
		t.Errorf("expected explicit verify-failure note:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "NOT confirmed") {
		t.Errorf("failure note must forbid claiming confirmation:\n%s", res.ForLLM)
	}
}
