// squash-ide-mcp is a minimal MCP (Model Context Protocol) server that
// receives status updates from a spawned Claude Code session and writes
// them to a shared status directory for the squash-ide TUI to poll.
//
// It communicates over stdio using JSON-RPC 2.0, exposing a single tool:
// squash_status.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/squashbox/squash-ide/internal/status"
)

// version is stamped at build time via -ldflags.
var version = "dev"

// --- JSON-RPC types --------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP content types -----------------------------------------------------

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// --- Tool call params ------------------------------------------------------

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type statusArgs struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// --- Server ----------------------------------------------------------------

func main() {
	taskID := os.Getenv("SQUASH_TASK_ID")
	if taskID == "" {
		fmt.Fprintln(os.Stderr, "squash-ide-mcp: SQUASH_TASK_ID not set")
		os.Exit(1)
	}

	scanner := bufio.NewScanner(os.Stdin)
	// MCP messages can be large; raise the buffer limit.
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			fmt.Fprintf(os.Stderr, "squash-ide-mcp: bad request: %v\n", err)
			continue
		}

		resp := handleRequest(taskID, &req)
		if resp == nil {
			continue // notification — no response
		}

		out, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "squash-ide-mcp: marshal error: %v\n", err)
			continue
		}
		fmt.Println(string(out))
	}
}

func handleRequest(taskID string, req *jsonRPCRequest) *jsonRPCResponse {
	// Notifications (no id) don't get responses.
	if req.ID == nil || string(req.ID) == "null" {
		// Auto-report working on initialized — the session was spawned to
		// do a task, so "working" is the correct initial state. Claude will
		// override this with explicit squash_status calls as it progresses.
		if req.Method == "notifications/initialized" {
			fmt.Fprintln(os.Stderr, "squash-ide-mcp: initialized — reporting working")
			_ = status.Write(taskID, "working", "Starting up")
		}
		return nil
	}

	switch req.Method {
	case "initialize":
		return success(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "squash-ide-mcp",
				"version": version,
			},
		})

	case "tools/list":
		return success(req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "squash_status",
					"description": "Report your current status to the squash-ide dashboard. Call this when your activity phase changes (starting work, running tests, going idle). Note: tool-permission dialogs (e.g. 'Do you want to make this edit?') are handled automatically by Claude Code hooks — you do not need to report input_required for those.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"state": map[string]any{
								"type":        "string",
								"enum":        []string{"idle", "working", "input_required", "testing"},
								"description": "Current state: idle (waiting/finished), working (reading/writing files), input_required (need user input or permission), testing (running tests)",
							},
							"message": map[string]any{
								"type":        "string",
								"description": "Brief description of current activity (max 80 chars)",
							},
						},
						"required": []string{"state", "message"},
					},
				},
			},
		})

	case "tools/call":
		return handleToolCall(taskID, req)

	case "ping":
		return success(req.ID, map[string]any{})

	default:
		return errResp(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func handleToolCall(taskID string, req *jsonRPCRequest) *jsonRPCResponse {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResp(req.ID, -32602, "invalid params")
	}

	if params.Name != "squash_status" {
		return success(req.ID, mcpToolResult{
			Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", params.Name)}},
			IsError: true,
		})
	}

	var args statusArgs
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return errResp(req.ID, -32602, "invalid arguments for squash_status")
	}

	if err := status.Write(taskID, args.State, args.Message); err != nil {
		return errResp(req.ID, -32000, fmt.Sprintf("status write failed: %v", err))
	}

	fmt.Fprintf(os.Stderr, "squash-ide-mcp: %s → %s: %s\n", taskID, args.State, args.Message)

	// Fire desktop notification for input_required, drop the dedup marker
	// on every other transition so the next input_required is fresh.
	if args.State == "input_required" {
		status.NotifyInputRequired(taskID, args.Message)
	} else {
		_ = status.RemoveNotify(taskID)
	}

	return success(req.ID, mcpToolResult{
		Content: []mcpContent{{
			Type: "text",
			Text: fmt.Sprintf("Status reported: %s — %s", args.State, args.Message),
		}},
	})
}

func success(id json.RawMessage, result any) *jsonRPCResponse {
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id json.RawMessage, code int, msg string) *jsonRPCResponse {
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: msg}}
}
