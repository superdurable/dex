#!/bin/bash
# Usage: age.sh SCENARIO CONCURRENCY WARMUP DURATION
set -u
out=/v/age/$1-c$2-w$3${AGE_TAG:-}; rm -rf $out /tmp/zd; mkdir -p $out
/v/dex-server-zd dev --data-dir /tmp/zd --config ${ZD_CONFIG:-/v/zd.yaml} > $out/server.log 2>&1 &
server=$!
/v/dexbench wait-ready --address 127.0.0.1:8801 --timeout 60s >/dev/null
/v/dexbench run --address 127.0.0.1:8801 --metrics-url http://127.0.0.1:9901/metrics --duration $4 --warmup $3 \
  --concurrency $2 --connections 1 --scenarios $1 --mode local --out $out > $out/run.log 2>&1
jq -r '"\(.config.scenario) C=\(.config.concurrency) warmup=\(.config.warmup_seconds)s: \(.throughput_per_second|floor) \(.throughput_unit)/s p50=\(.latency.p50_ms) ms"' $out/*.json
kill $server; wait $server 2>/dev/null
