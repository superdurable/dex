#!/bin/bash

for run in {1..120}; do
  if cadence --do default domain describe >/dev/null 2>&1; then
    break
  fi
  cadence --do default domain register || true
  sleep 1
done

tail -f /dev/null
