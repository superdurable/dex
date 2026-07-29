#!/usr/bin/env bash

set -u

while read -r container_id container_name; do
  echo "::group::Docker logs: ${container_name}"
  docker logs "${container_id}" 2>&1 || true
  echo "::endgroup::"
done < <(docker ps --all --format '{{.ID}} {{.Names}}')
