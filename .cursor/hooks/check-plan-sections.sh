#!/usr/bin/env bash
# Plan-mode enforcement hook.
#
# Triggered as a postToolUse hook for the CreatePlan tool. Reads the tool
# invocation JSON from stdin, extracts the plan body, and warns (via the
# `additional_context` field) when any of the required top-level sections
# are missing.
#
# Warn-only by design: the hook never blocks; it surfaces a message in the
# agent's next turn so the model is reminded to fix the plan immediately.
# When the input JSON shape is unexpected, the hook fails open (no warning)
# rather than producing false positives.
#
# See:
#   .cursor/rules/plan-mode-checklist.mdc  (canonical list of required sections)
#   .cursor/hooks.json                      (hook registration)

set -euo pipefail

emit_noop() {
  printf '{}'
  exit 0
}

input=$(cat)

# Parse the plan body. The exact JSON shape under postToolUse for CreatePlan
# may evolve; tolerate the two most likely paths and fall back to the raw
# input as a last resort. If neither yields content, fail open.
plan=""
if command -v jq >/dev/null 2>&1; then
  plan=$(printf '%s' "$input" | jq -r '
    .tool_input.plan
    // .tool_call.parameters.plan
    // .tool_input.content
    // .tool_call.parameters.content
    // empty
  ' 2>/dev/null || true)
elif command -v python3 >/dev/null 2>&1; then
  plan=$(printf '%s' "$input" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
def dig(*path):
    cur = d
    for k in path:
        if not isinstance(cur, dict) or k not in cur:
            return None
        cur = cur[k]
    return cur
for path in (("tool_input","plan"), ("tool_call","parameters","plan"),
             ("tool_input","content"), ("tool_call","parameters","content")):
    v = dig(*path)
    if isinstance(v, str) and v:
        print(v)
        break
' 2>/dev/null || true)
else
  emit_noop
fi

if [ -z "${plan:-}" ]; then
  emit_noop
fi

required=("## Tests" "## Metrics" "## Logging" "## Documentation" "## UI/UX")
missing=()
for h in "${required[@]}"; do
  if ! grep -qF -- "$h" <<<"$plan"; then
    missing+=("$h")
  fi
done

if [ "${#missing[@]}" -eq 0 ]; then
  emit_noop
fi

list=$(printf -- '- %s\n' "${missing[@]}")
msg=$(printf 'Plan is missing the following required sections (per .cursor/rules/plan-mode-checklist.mdc):\n%s\nEdit the plan file to add them before continuing. The "N/A: <reason>" form is acceptable when a section truly does not apply.' "$list")

if command -v jq >/dev/null 2>&1; then
  jq -n --arg m "$msg" '{additional_context: $m}'
else
  python3 -c 'import json,sys; print(json.dumps({"additional_context": sys.argv[1]}))' "$msg"
fi
