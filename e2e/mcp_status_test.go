//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squashbox/squash-ide/internal/status"
)

// TestMCP_InitializeThenStatus is the Go port of scripts/test-mcp-cycle.sh.
// It starts the squash-ide-mcp binary as a subprocess, drives a JSON-RPC
// handshake on stdio (initialize → tools/list → tools/call squash_status),
// and asserts each response.
func TestMCP_InitializeThenStatus(t *testing.T) {
	// Build squash-ide-mcp into a temp path.
	tmp := t.TempDir()
	mcpBin := filepath.Join(tmp, "squash-ide-mcp")
	build := exec.Command("go", "build", "-o", mcpBin, "../cmd/squash-ide-mcp")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	c := exec.Command(mcpBin)
	c.Env = append(c.Env, "SQUASH_TASK_ID=T-mcp-test")
	stdin, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = c.Wait()
	}()

	rd := bufio.NewReader(stdout)

	// --- initialize ---------------------------------------------------
	must(t, stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	resp := readResponse(t, rd)
	if resp["error"] != nil {
		t.Fatalf("initialize error: %+v", resp["error"])
	}
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocol: %v", result["protocolVersion"])
	}

	// --- tools/list ---------------------------------------------------
	must(t, stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	resp = readResponse(t, rd)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("got %d tools", len(tools))
	}
	if (tools[0].(map[string]any))["name"] != "squash_status" {
		t.Errorf("tool name: %v", tools[0])
	}

	// --- tools/call squash_status (no stage — backward compat) --------
	must(t, stdin,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"squash_status","arguments":{"state":"working","message":"hello"}}}`)
	resp = readResponse(t, rd)
	if resp["error"] != nil {
		t.Fatalf("tool error: %+v", resp["error"])
	}

	// --- tools/call squash_status WITH stage (T-053) ------------------
	t.Cleanup(func() {
		_ = status.Remove("T-mcp-test")
		_ = status.RemoveStage("T-mcp-test")
	})
	must(t, stdin,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"squash_status","arguments":{"state":"working","message":"coding","stage":"implementation"}}}`)
	resp = readResponse(t, rd)
	if resp["error"] != nil {
		t.Fatalf("stage tool error: %+v", resp["error"])
	}
	// The binary writes to the real status dir; ReadAll merges the stage file.
	all, err := status.ReadAll()
	if err != nil {
		t.Fatalf("status.ReadAll: %v", err)
	}
	if got := all["T-mcp-test"].Stage; got != "implementation" {
		t.Errorf("merged stage = %q, want implementation", got)
	}
}

func must(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readResponse(t *testing.T, rd *bufio.Reader) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return out
	}
	t.Fatal("timeout waiting for response")
	return nil
}
