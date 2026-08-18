#!/usr/bin/env bash

# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.

set -euo pipefail

port="${PLAYGROUND_PORT:-3333}"
example_server="${PLAYGROUND_EXAMPLE_SERVER:-http://127.0.0.1:8080}"
dex_web="${PLAYGROUND_DEX_WEB:-http://127.0.0.1:8802}"
is_print_config=0
bind_host="127.0.0.1"

usage() {
  cat <<'EOF'
Usage: ./start.sh [options]

  --port PORT             Playground listen port (default 3333, env PLAYGROUND_PORT)
  --example-server URL    Example server base URL (default http://127.0.0.1:8080,
                          env PLAYGROUND_EXAMPLE_SERVER)
  --dex-web URL           Dex Web base URL (default http://127.0.0.1:8802,
                          env PLAYGROUND_DEX_WEB)
  --print-config          Print the three URLs and exit
  -h, --help              Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port)
      port="$2"
      shift 2
      ;;
    --example-server)
      example_server="$2"
      shift 2
      ;;
    --dex-web)
      dex_web="$2"
      shift 2
      ;;
    --print-config)
      is_print_config=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

root="$(cd "$(dirname "$0")" && pwd)"
playground_url="http://${bind_host}:${port}"

print_urls() {
  printf 'Playground:     %s\n' "$playground_url"
  printf 'Example server: %s\n' "$example_server"
  printf 'Dex Web:        %s\n' "$dex_web"
}

# Skip when CI, non-interactive, or no opener — smoke/CI must not fail.
should_open_browser() {
  if [[ -n "${CI:-}" ]]; then
    return 1
  fi
  if [[ ! -t 0 || ! -t 1 ]]; then
    return 1
  fi
  command -v open >/dev/null 2>&1 || command -v xdg-open >/dev/null 2>&1
}

wait_until_listening() {
  local pid="$1"
  python3 - "$pid" "$bind_host" "$port" <<'PY'
import os
import socket
import sys
import time

pid = int(sys.argv[1])
host = sys.argv[2]
port = int(sys.argv[3])
deadline = time.monotonic() + 5
while time.monotonic() < deadline:
    try:
        os.kill(pid, 0)
    except OSError:
        raise SystemExit(1)
    try:
        with socket.create_connection((host, port), timeout=0.2):
            raise SystemExit(0)
    except OSError:
        time.sleep(0.05)
raise SystemExit(1)
PY
}

open_playground_url() {
  if ! should_open_browser; then
    return 0
  fi
  if command -v open >/dev/null 2>&1; then
    open "$playground_url" >/dev/null 2>&1 || true
  else
    (xdg-open "$playground_url" >/dev/null 2>&1 || true) &
  fi
}

print_urls

if [[ "$is_print_config" -eq 1 ]]; then
  exit 0
fi

python3 - "$root" "$example_server" "$dex_web" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
config_path = root / "config.js"
payload = {
    "exampleServer": sys.argv[2],
    "dexWeb": sys.argv[3],
}
config_path.write_text(
    "window.PLAYGROUND_CONFIG = " + json.dumps(payload, indent=2) + ";\n",
    encoding="utf-8",
)
PY

echo "Serving ${root} (Ctrl+C to stop)"
cd "$root"
python3 -m http.server "$port" --bind "$bind_host" &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true' EXIT INT TERM

if wait_until_listening "$server_pid"; then
  open_playground_url
fi

wait "$server_pid"
trap - EXIT INT TERM

