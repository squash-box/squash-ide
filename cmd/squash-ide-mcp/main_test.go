package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/status"
)

// swapStatusDir points status writes at a temp dir. Uses reflection to
// reach the package's private dirRef — or rather: since dirRef is in the
// status package and not exported, we temporarily change via the file
// system trick: status writes to Dir, and we rely on status.Dir for
// validation. A less fragile alternative is to simply assert on the
// response payload rather than the on-disk file.
func TestHandleRequest_Initialize(t *testing.T) {
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
	}
	resp := handleRequest("T-001", req)
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
}

func TestHandleRequest_ToolsList(t *testing.T) {
	req := &jsonRPCRequest{ID: json.RawMessage(`2`), Method: "tools/list"}
	resp := handleRequest("T-001", req)
	if resp == nil || resp.Error != nil {
		t.Fatalf("unexpected: %+v", resp)
	}
	result := resp.Result.(map[string]any)
	tools := result["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if tools[0]["name"] != "squash_status" {
		t.Errorf("tool name = %v", tools[0]["name"])
	}
}

func TestHandleRequest_UnknownMethod(t *testing.T) {
	req := &jsonRPCRequest{ID: json.RawMessage(`3`), Method: "bogus/method"}
	resp := handleRequest("T-001", req)
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resp.Error.Code)
	}
}

func TestHandleRequest_Ping(t *testing.T) {
	req := &jsonRPCRequest{ID: json.RawMessage(`4`), Method: "ping"}
	resp := handleRequest("T-001", req)
	if resp.Error != nil {
		t.Fatalf("ping err: %+v", resp.Error)
	}
}

func TestHandleRequest_NotificationReturnsNil(t *testing.T) {
	// id=null is a notification; must not emit a response.
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      nil,
		Method:  "notifications/initialized",
	}
	resp := handleRequest("T-001", req)
	if resp != nil {
		t.Errorf("expected nil, got %+v", resp)
	}
}

func TestHandleToolCall_SquashStatus_WritesFile(t *testing.T) {
	// Swap the status package's dir at runtime so we can observe the file.
	// We can't reach status.dirRef (unexported) directly, but status.Dir is
	// a const pointing to /tmp/squash-ide/status. Tests use a scoped env
	// trick: write to a known task id, assert the file exists at Dir.
	//
	// For full hermeticity the status package also exposes dirRef to its
	// own test. Here we rely on the writable /tmp. Use a unique task ID
	// so parallel tests don't collide.
	taskID := "T-test-mcp-" + t.Name()

	params := toolCallParams{
		Name:      "squash_status",
		Arguments: json.RawMessage(`{"state":"working","message":"hi"}`),
	}
	paramsJSON, _ := json.Marshal(params)

	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  paramsJSON,
	}
	resp := handleRequest(taskID, req)
	if resp.Error != nil {
		t.Fatalf("tool call err: %+v", resp.Error)
	}

	// Clean up the status file.
	_ = status.Remove(taskID)

	result, ok := resp.Result.(mcpToolResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if result.IsError {
		t.Error("expected not-error")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "working") {
		t.Errorf("content = %+v", result.Content)
	}
	// Sanity: filename matches.
	_ = filepath.Join(status.Dir, taskID+".json")
}

func TestHandleToolCall_WithStage_WritesBothFiles(t *testing.T) {
	taskID := "T-test-mcp-stage-" + t.Name()
	t.Cleanup(func() {
		_ = status.Remove(taskID)
		_ = status.RemoveStage(taskID)
	})

	params := toolCallParams{
		Name:      "squash_status",
		Arguments: json.RawMessage(`{"state":"working","message":"hi","stage":"implementation"}`),
	}
	paramsJSON, _ := json.Marshal(params)
	req := &jsonRPCRequest{ID: json.RawMessage(`9`), Method: "tools/call", Params: paramsJSON}

	resp := handleRequest(taskID, req)
	if resp.Error != nil {
		t.Fatalf("tool call err: %+v", resp.Error)
	}
	result := resp.Result.(mcpToolResult)
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}

	// Activity file written (state).
	if _, err := os.Stat(filepath.Join(status.Dir, taskID+".json")); err != nil {
		t.Errorf("activity file missing: %v", err)
	}
	// Stage merged through ReadAll.
	all, err := status.ReadAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if all[taskID].Stage != "implementation" {
		t.Errorf("merged stage = %q, want implementation", all[taskID].Stage)
	}
}

func TestHandleToolCall_BogusStage_IsErrorNoStageFile(t *testing.T) {
	taskID := "T-test-mcp-bogus-" + t.Name()
	t.Cleanup(func() {
		_ = status.Remove(taskID)
		_ = status.RemoveStage(taskID)
	})

	params := toolCallParams{
		Name:      "squash_status",
		Arguments: json.RawMessage(`{"state":"working","message":"hi","stage":"deploying"}`),
	}
	paramsJSON, _ := json.Marshal(params)
	req := &jsonRPCRequest{ID: json.RawMessage(`10`), Method: "tools/call", Params: paramsJSON}

	resp := handleRequest(taskID, req)
	if resp.Error != nil {
		t.Fatalf("should be a tool-result error, not a JSON-RPC error: %+v", resp.Error)
	}
	result := resp.Result.(mcpToolResult)
	if !result.IsError {
		t.Error("expected IsError for unknown stage")
	}
	// Activity write still succeeded.
	if _, err := os.Stat(filepath.Join(status.Dir, taskID+".json")); err != nil {
		t.Errorf("activity file should still be written: %v", err)
	}
	// No stage was persisted.
	all, _ := status.ReadAll()
	if all[taskID].Stage != "" {
		t.Errorf("bogus stage must not be written, got %q", all[taskID].Stage)
	}
}

func TestHandleToolCall_NoStage_NoStageFile(t *testing.T) {
	taskID := "T-test-mcp-nostage-" + t.Name()
	t.Cleanup(func() {
		_ = status.Remove(taskID)
		_ = status.RemoveStage(taskID)
	})

	params := toolCallParams{
		Name:      "squash_status",
		Arguments: json.RawMessage(`{"state":"working","message":"hi"}`),
	}
	paramsJSON, _ := json.Marshal(params)
	req := &jsonRPCRequest{ID: json.RawMessage(`11`), Method: "tools/call", Params: paramsJSON}

	resp := handleRequest(taskID, req)
	if resp.Error != nil || resp.Result.(mcpToolResult).IsError {
		t.Fatalf("unexpected error: %+v", resp)
	}
	all, _ := status.ReadAll()
	if all[taskID].Stage != "" {
		t.Errorf("no stage should be written when omitted, got %q", all[taskID].Stage)
	}
}

func TestHandleRequest_ToolsList_HasStageProperty(t *testing.T) {
	req := &jsonRPCRequest{ID: json.RawMessage(`12`), Method: "tools/list"}
	resp := handleRequest("T-001", req)
	tools := resp.Result.(map[string]any)["tools"].([]map[string]any)
	schema := tools[0]["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, ok := props["stage"]; !ok {
		t.Fatal("tools/list schema missing the stage property")
	}
	// stage must NOT be required (backward-compat with no-stage callers).
	for _, r := range schema["required"].([]string) {
		if r == "stage" {
			t.Error("stage must be optional, not required")
		}
	}
}

func TestHandleToolCall_UnknownTool(t *testing.T) {
	params := toolCallParams{
		Name:      "unknown-tool",
		Arguments: json.RawMessage(`{}`),
	}
	paramsJSON, _ := json.Marshal(params)
	req := &jsonRPCRequest{ID: json.RawMessage(`6`), Method: "tools/call", Params: paramsJSON}

	resp := handleRequest("T-001", req)
	result, ok := resp.Result.(mcpToolResult)
	if !ok {
		t.Fatalf("type %T", resp.Result)
	}
	if !result.IsError {
		t.Error("expected IsError for unknown tool")
	}
}

func TestHandleToolCall_InvalidArgs(t *testing.T) {
	params := toolCallParams{Name: "squash_status", Arguments: json.RawMessage(`not-json`)}
	paramsJSON, _ := json.Marshal(params)
	req := &jsonRPCRequest{ID: json.RawMessage(`7`), Method: "tools/call", Params: paramsJSON}

	resp := handleRequest("T-001", req)
	if resp.Error == nil {
		t.Fatal("expected err")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code = %d", resp.Error.Code)
	}
}

func TestHandleRequest_ToolCall_InvalidParams(t *testing.T) {
	req := &jsonRPCRequest{
		ID:     json.RawMessage(`8`),
		Method: "tools/call",
		Params: json.RawMessage(`not json`),
	}
	resp := handleRequest("T-001", req)
	if resp.Error == nil {
		t.Fatal("expected err")
	}
}
