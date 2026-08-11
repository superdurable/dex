#!/bin/bash

for run in {1..60}; do
  if tctl namespace describe default >/dev/null 2>&1; then
    break
  fi
  tctl namespace register default || true
  sleep 1
done

tail -f /dev/null
