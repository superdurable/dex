#!/bin/bash

# Start MinIO in the background
mkdir -p /tmp/minio-data
export MINIO_ROOT_USER=minioadmin
export MINIO_ROOT_PASSWORD=minioadmin
minio server /tmp/minio-data --address ":9000" --console-address ":9001" &

# Wait for MinIO to be ready
echo "waiting for MinIO to start..."
for run in {1..30}; do
  sleep 1
  if curl -s http://localhost:9000/minio/health/ready > /dev/null 2>&1; then
    echo "MinIO is ready"
    break
  fi
done

export PATH="$PATH:/root/.temporalio/bin"
temporal server start-dev --ip 0.0.0.0 --ui-ip 0.0.0.0 &
echo "temporal server started..."

/dex/dex-server start
