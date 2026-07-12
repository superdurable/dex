#!/bin/bash
# Regenerate all metered store wrappers using gowrap.
#
# Usage:
#   ./generate.sh
#
# Prerequisites:
#   go install github.com/hexdigest/gowrap/cmd/gowrap@latest

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

if ! command -v gowrap &> /dev/null; then
    echo "Error: gowrap is not installed. Install it with:"
    echo "  go install github.com/hexdigest/gowrap/cmd/gowrap@latest"
    exit 1
fi

STORE_PKG="github.com/superdurable/dex/server/internal/persistence"

# ====================================================================
# Generate metered wrappers for all stores
# ====================================================================

echo "Generating RunStoreWithMetrics..."
gowrap gen \
    -p "$STORE_PKG" \
    -i RunStore \
    -t ./metered.tmpl \
    -o ./run_store_with_metrics.go \
    -v InterfacePkg=persistence \
    -v InterfaceImport="$STORE_PKG"

echo "Generating ShardStoreWithMetrics..."
gowrap gen \
    -p "$STORE_PKG" \
    -i ShardStore \
    -t ./metered.tmpl \
    -o ./shard_store_with_metrics.go \
    -v InterfacePkg=persistence \
    -v InterfaceImport="$STORE_PKG"

echo "Generating BlobStoreWithMetrics..."
gowrap gen \
    -p "$STORE_PKG" \
    -i BlobStore \
    -t ./metered.tmpl \
    -o ./blob_store_with_metrics.go \
    -v InterfacePkg=persistence \
    -v InterfaceImport="$STORE_PKG"

echo "Generating TasklistStoreWithMetrics..."
gowrap gen \
    -p "$STORE_PKG" \
    -i TasklistStore \
    -t ./metered.tmpl \
    -o ./tasklist_store_with_metrics.go \
    -v InterfacePkg=persistence \
    -v InterfaceImport="$STORE_PKG"

echo "Done! Verifying build..."
cd "$SCRIPT_DIR/.."
go build ./wrappers/...
echo "Build successful!"
