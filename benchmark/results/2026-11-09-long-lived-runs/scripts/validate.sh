#!/bin/bash
# Usage: validate.sh SCENARIO CONCURRENCY DURATION ; runs one scenario on a fresh dev server.
set -u
scenario=$1 concurrency=$2 duration=$3
out=/v/out/$scenario-c$concurrency; rm -rf $out /tmp/zd; mkdir -p $out
/v/dex-server-zd dev --data-dir /tmp/zd --config /v/zd.yaml > $out/server.log 2>&1 &
server=$!
/v/dexbench wait-ready --address 127.0.0.1:8801 --timeout 60s || { tail $out/server.log; exit 1; }
curl -s 127.0.0.1:9901/metrics > $out/metrics-before.txt
/v/dexbench run --address 127.0.0.1:8801 --metrics-url http://127.0.0.1:9901/metrics --duration $duration --warmup 2 \
  --concurrency $concurrency --connections 1 --scenarios $scenario --mode local --out $out > $out/run.log 2>&1
echo "dexbench exit=$?"
sleep 20
curl -s 127.0.0.1:9901/metrics > $out/metrics-after.txt
kill $server; wait $server 2>/dev/null
