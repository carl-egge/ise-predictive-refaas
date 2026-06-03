#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
export ROOT_DIR
FLOCI_ENABLED=1

if [[ "${1:-}" == "--no-floci" ]]; then
  FLOCI_ENABLED=0
  shift
fi

if [[ ! -f "$ROOT_DIR/.env" ]]; then
  echo "Missing .env at $ROOT_DIR/.env" >&2
  exit 1
fi

status=$(
  ROOT_DIR="$ROOT_DIR" FLOCI_ENABLED="$FLOCI_ENABLED" \
  python3 - <<'PY' | curl -s -o /tmp/reconfigure-gemini.out -w "%{http_code}" \
    -X POST -H "Content-Type: application/json" --data-binary @- http://localhost:8080/reconfigure
import json
import os
import sys

root = os.environ.get("ROOT_DIR", ".")

def load_env(path):
    env = {}
    with open(path, "r", encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, val = line.split("=", 1)
            val = val.strip().strip('"').strip("'")
            env[key.strip()] = val
    return env

env = load_env(os.path.join(root, ".env"))
key = env.get("GEMINI_API_KEY")
if not key:
    print("GEMINI_API_KEY missing from .env", file=sys.stderr)
    sys.exit(1)

with open(os.path.join(root, "default.json"), "r", encoding="utf-8") as fh:
    data = json.load(fh)

model = env.get("GEMINI_MODEL", "gemini-2.5-flash")
floci_enabled = os.environ.get("FLOCI_ENABLED", "1") == "1"

if "pipeline" in data and "options" in data["pipeline"]:
    data["pipeline"]["options"]["floci_enabled"] = floci_enabled
    if floci_enabled:
        data["pipeline"]["options"]["floci_endpoint"] = "http://floci:4566"

data["LLMClient"] = "gemini"
data["args"] = {
    "GEMINI_API_KEY": key,
    "GEMINI_MODEL": model,
}

print(json.dumps(data))
PY
)

if [[ "$status" != "201" ]]; then
  echo "Pipeline reconfigure failed (status $status)." >&2
  if [[ -s /tmp/reconfigure-gemini.out ]]; then
    cat /tmp/reconfigure-gemini.out >&2
  fi
  exit 1
fi

echo "Pipeline configured for Gemini (floci_enabled=$FLOCI_ENABLED)."