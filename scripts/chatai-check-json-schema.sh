#!/usr/bin/env bash
# Validates whether a given GWDG Chat AI model honors response_format
# json_schema with a fixed (non-dynamic) array-of-objects schema.
#
# Usage: ./check_json_schema.sh <model-name>
# Requires: .env in the same directory with ACADEMIC_CLOUD_API_KEY=<key>

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
ENV_FILE="$SCRIPT_DIR/../.env"
BASE_URL="https://chat-ai.academiccloud.de/v1"

usage() {
  echo "Usage: $0 <model-name>" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "Error: '$1' is required but not installed." >&2; exit 1; }
}

[[ $# -eq 1 ]] || usage
MODEL="$1"

require_cmd curl
require_cmd jq

[[ -f "$ENV_FILE" ]] || { echo "Error: .env not found at $ENV_FILE" >&2; exit 1; }

# Load only the key we need, ignore comments/blank lines, tolerate quotes.
API_KEY="$(grep -E '^ACADEMIC_CLOUD_API_KEY=' "$ENV_FILE" | tail -n1 | cut -d'=' -f2- | tr -d '"'"'"'\r')"
[[ -n "$API_KEY" ]] || { echo "Error: ACADEMIC_CLOUD_API_KEY not set in $ENV_FILE" >&2; exit 1; }

# Fixed schema: strict-mode legal, no dynamic keys.
SCHEMA=$(cat <<'EOF'
{
  "type": "object",
  "properties": {
    "files": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "filename": {"type": "string"},
          "content": {"type": "string"}
        },
        "required": ["filename", "content"],
        "additionalProperties": false
      }
    }
  },
  "required": ["files"],
  "additionalProperties": false
}
EOF
)

REQUEST_BODY=$(jq -n \
  --arg model "$MODEL" \
  --argjson schema "$SCHEMA" \
  '{
    model: $model,
    messages: [
      {role: "system", content: "Translate code between languages."},
      {role: "user", content: "Translate this Python function to Go: def add(a,b): return a+b"}
    ],
    response_format: {
      type: "json_schema",
      json_schema: {
        name: "file_list",
        strict: true,
        schema: $schema
      }
    },
    temperature: 0
  }')

HTTP_STATUS=$(curl -sS -o /tmp/json_schema_check_response.json -w '%{http_code}' \
  -X POST "$BASE_URL/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d "$REQUEST_BODY") || { echo "Error: request failed" >&2; exit 1; }

RESPONSE_BODY="$(cat /tmp/json_schema_check_response.json)"

if [[ "$HTTP_STATUS" -ne 200 ]]; then
  echo "FAIL: model '$MODEL' returned HTTP $HTTP_STATUS" >&2
  echo "$RESPONSE_BODY" | jq . >&2 2>/dev/null || echo "$RESPONSE_BODY" >&2
  exit 1
fi

CONTENT=$(echo "$RESPONSE_BODY" | jq -r '.choices[0].message.content // empty')
if [[ -z "$CONTENT" ]]; then
  echo "FAIL: model '$MODEL' returned no message content" >&2
  echo "$RESPONSE_BODY" | jq . >&2
  exit 1
fi

# Validate: content must be valid JSON matching the required shape,
# i.e. an object with a non-empty "files" array of {filename, content}.
if ! echo "$CONTENT" | jq -e '
    type == "object"
    and has("files")
    and (.files | type == "array")
    and (.files | length > 0)
    and (.files | all(type == "object" and has("filename") and has("content")
                       and (.filename | type == "string")
                       and (.content | type == "string")))
  ' >/dev/null 2>&1; then
  echo "FAIL: model '$MODEL' did not honor json_schema (invalid or non-conformant output)" >&2
  echo "--- raw content ---" >&2
  echo "$CONTENT" >&2
  exit 1
fi

echo "PASS: model '$MODEL' honors json_schema (fixed array) response_format"
echo "--- validated output ---"
echo "$CONTENT" | jq .