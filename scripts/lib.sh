#!/usr/bin/env bash
# Shared helpers for create-agent.sh. Sourced, not executed directly.
#
# All interactive prompts are built on `gum` (https://github.com/charmbracelet/gum).
# require_gum must be called once, early, before any prompt_* function is used.

require_gum() {
  if ! command -v gum >/dev/null 2>&1; then
    cat >&2 <<'EOF'
error: gum is not installed.

Install it with one of:
  brew install gum
  go install github.com/charmbracelet/gum@latest
  (or grab a release binary from https://github.com/charmbracelet/gum/releases)
EOF
    exit 1
  fi
}

# prompt_required <var_name> <header_text> [placeholder]
# Keeps asking until a non-empty value is given. Ctrl+C aborts the script.
prompt_required() {
  local __var="$1" __header="$2" __placeholder="${3:-}" __val=""
  while [ -z "$__val" ]; do
    __val="$(gum input --header "$__header" --placeholder "$__placeholder")"
    if [ -z "$__val" ]; then
      gum style --foreground 1 "  required, try again"
    fi
  done
  printf -v "$__var" '%s' "$__val"
}

# prompt_default <var_name> <header_text> <default_value>
# Pre-fills the input with the default; user can edit or accept as-is.
prompt_default() {
  local __var="$1" __header="$2" __default="$3" __val=""
  __val="$(gum input --header "$__header" --value "$__default")"
  if [ -z "$__val" ]; then
    __val="$__default"
  fi
  printf -v "$__var" '%s' "$__val"
}

# prompt_choice <var_name> <header_text> <default> <choice1> <choice2> ...
# Renders an arrow-key selectable list via `gum choose`, cursor starts on <default>.
prompt_choice() {
  local __var="$1" __header="$2" __default="$3"
  shift 3
  local __choices=("$@")
  local __val
  __val="$(gum choose --header "$__header" --selected "$__default" "${__choices[@]}")"
  printf -v "$__var" '%s' "$__val"
}

# confirm_overwrite <file_path>
# Returns 0 (true) if the user confirms overwrite, 1 otherwise.
confirm_overwrite() {
  gum confirm "$1 already exists. Overwrite?"
}

# json_escape <string>
# Minimal JSON string escaping (backslash, double-quote, control chars).
json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\t'/\\t}"
  printf '%s' "$s"
}

# json_string_array <word1> <word2> ...
# Renders a bash array as a JSON array of strings, e.g. ["a","b"].
json_string_array() {
  local out="["
  local first=1
  for w in "$@"; do
    if [ $first -eq 0 ]; then out+=","; fi
    out+="\"$(json_escape "$w")\""
    first=0
  done
  out+="]"
  printf '%s' "$out"
}
