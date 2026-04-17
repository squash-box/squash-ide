#!/usr/bin/env bash
# test-mcp-cycle.sh — fake Claude session that cycles through MCP states.
#
# Usage (standalone):
#   ./scripts/test-mcp-cycle.sh T-012
#
# Usage (as spawn command via TUI — must use absolute path):
#   squash-ide --spawn-cmd "$PWD/scripts/test-mcp-cycle.sh"
#   Then spawn any backlog task normally — pane runs this instead of claude.
#
# The script starts squash-ide-mcp as a coprocess, handshakes, and loops
# through: working → testing → idle → input_required → (repeat).

set -uo pipefail
# Keep the pane open on error so the user can read what went wrong.
trap 'echo; echo "ERROR — press enter to close"; read -r' ERR

# Accept task_id from first arg. The default spawn args template is
# "/implement {task_id}", so strip the "/implement " prefix if present.
RAW="${1:-T-999}"
TASK_ID="${RAW#/implement }"

# Find the MCP binary: check next to this script, then in PATH.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MCP_BIN="${SCRIPT_DIR}/../bin/squash-ide-mcp"
if [[ ! -x "$MCP_BIN" ]]; then
  MCP_BIN="$(command -v squash-ide-mcp 2>/dev/null || true)"
fi
if [[ -z "$MCP_BIN" ]]; then
  echo "ERROR: squash-ide-mcp not found. Run 'make build' first."
  read -rp "Press enter to exit..."
  exit 1
fi

# Colours for terminal output.
C_RESET="\033[0m"
C_BOLD="\033[1m"
C_DIM="\033[2m"
C_GREEN="\033[32m"
C_CYAN="\033[36m"
C_YELLOW="\033[33m"
C_PINK="\033[35m"

banner() {
  local color="$1" icon="$2" label="$3" msg="$4"
  printf "\n${C_BOLD}${color}  %s  %s${C_RESET}  ${C_DIM}%s${C_RESET}\n" "$icon" "$label" "$msg"
}

# Start MCP server as a coprocess.
export SQUASH_TASK_ID="$TASK_ID"
coproc MCP { "$MCP_BIN" 2>&1; }

# Send a JSON-RPC message to the MCP server and read the response.
mcp_send() {
  echo "$1" >&"${MCP[1]}"
}

mcp_read() {
  read -r -t 5 line <&"${MCP[0]}" 2>/dev/null || true
  echo "$line"
}

# Handshake.
printf "${C_BOLD}${C_GREEN}squash-ide MCP test harness${C_RESET}\n"
printf "${C_DIM}Task: %s | MCP: %s${C_RESET}\n" "$TASK_ID" "$MCP_BIN"
printf "${C_DIM}─────────────────────────────────────${C_RESET}\n"

mcp_send '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
mcp_read >/dev/null  # consume response

mcp_send '{"jsonrpc":"2.0","id":null,"method":"notifications/initialized","params":{}}'
sleep 0.5
# Read the stderr "initialized" line
mcp_read >/dev/null 2>/dev/null || true

banner "$C_GREEN" "●" "IDLE" "Session initialized"

# State definitions: state, icon, color, label, message, duration(s)
STATES=(
  "working|●|$C_GREEN|WORKING|Exploring codebase and reading files|5"
  "working|●|$C_GREEN|WORKING|Implementing feature changes|6"
  "testing|⧖|$C_CYAN|TESTING|Running go test ./...|4"
  "working|●|$C_GREEN|WORKING|Fixing test failures|4"
  "testing|⧖|$C_CYAN|TESTING|Re-running test suite|3"
  "working|●|$C_GREEN|WORKING|Preparing commit and PR|3"
  "idle|○|$C_YELLOW|IDLE|Waiting for next instruction|5"
  "input_required|⚠|$C_PINK|INPUT REQUIRED|Permission needed to push branch|6"
  "working|●|$C_GREEN|WORKING|Pushing branch and creating PR|4"
  "idle|○|$C_YELLOW|IDLE|Task complete|5"
)

ID=10
cycle=1

cleanup() {
  # Close the coprocess
  exec {MCP[1]}>&- 2>/dev/null || true
  wait "${MCP_PID}" 2>/dev/null || true
  printf "\n${C_DIM}Test harness exited.${C_RESET}\n"
}
trap cleanup EXIT

while true; do
  printf "\n${C_BOLD}── Cycle %d ──${C_RESET}\n" "$cycle"

  for entry in "${STATES[@]}"; do
    IFS='|' read -r state icon color label msg duration <<< "$entry"

    ID=$((ID + 1))
    mcp_send "$(printf '{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"squash_status","arguments":{"state":"%s","message":"%s"}}}' "$ID" "$state" "$msg")"
    mcp_read >/dev/null  # consume response

    banner "$color" "$icon" "$label" "$msg"

    # Countdown.
    for ((i=duration; i>0; i--)); do
      printf "\r${C_DIM}  next state in %ds...${C_RESET}  " "$i"
      sleep 1
    done
    printf "\r                              \r"
  done

  cycle=$((cycle + 1))
done
