#!/usr/bin/env bash
# Entry point wizard. Asks the user what they want in a container and
# writes specs/<name>.json. Does NOT create the container itself -
# that's agentd's job, invoked via agentctl.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
source "$SCRIPT_DIR/scripts/lib.sh"

require_gum

SPEC_DIR="$SCRIPT_DIR/specs"
mkdir -p "$SPEC_DIR"

gum style --bold --foreground 212 "=== agentbox: new container ==="
echo

prompt_required NAME "Agent name" "e.g. worker-a"

prompt_default BASE_ROOTFS "Base rootfs (path to a directory containing the container's filesystem)" "$SCRIPT_DIR/rootfs/alpine-min"
if [ ! -d "$BASE_ROOTFS" ]; then
  gum style --foreground 3 "  note: $BASE_ROOTFS does not exist yet - you'll need to populate it before this container can actually run"
fi

prompt_required ENTRY_COMMAND_RAW "Entry command - the PID 1 process, path must exist INSIDE base_rootfs" "e.g. /usr/bin/python3 /app/agent.py --mode worker"
# Split on whitespace into an array for the JSON array field.
read -r -a ENTRY_COMMAND_ARR <<< "$ENTRY_COMMAND_RAW"

prompt_default MEMORY_LIMIT "Memory limit (e.g. 256m, 1g, or 'unlimited')" "256m"

prompt_default CPU_LIMIT "CPU limit in cores (e.g. 0.5, 1, 2, or 'unlimited')" "0.5"

prompt_default PID_LIMIT "PID limit (max processes/threads inside container)" "64"

prompt_choice NETWORK_MODE "Network mode" "bridge" "bridge" "none" "host"

prompt_choice RESTART_POLICY "Restart policy" "on-failure" "never" "on-failure" "always"

SPEC_FILE="$SPEC_DIR/$NAME.json"

if [ -e "$SPEC_FILE" ]; then
  if ! confirm_overwrite "$SPEC_FILE"; then
    gum style --foreground 1 "Aborted."
    exit 1
  fi
fi

ENTRY_COMMAND_JSON="$(json_string_array "${ENTRY_COMMAND_ARR[@]}")"

cat > "$SPEC_FILE" <<EOF
{
  "version": 1,
  "name": "$(json_escape "$NAME")",
  "base_rootfs": "$(json_escape "$BASE_ROOTFS")",
  "entry_command": $ENTRY_COMMAND_JSON,
  "memory_limit": "$(json_escape "$MEMORY_LIMIT")",
  "cpu_limit": "$(json_escape "$CPU_LIMIT")",
  "pid_limit": $PID_LIMIT,
  "network_mode": "$(json_escape "$NETWORK_MODE")",
  "restart_policy": "$(json_escape "$RESTART_POLICY")"
}
EOF

echo
gum style --foreground 2 "Wrote $SPEC_FILE"
echo
echo "Next: validate it with"
gum style --foreground 212 "  go run ./cmd/agentctl validate $SPEC_FILE"
