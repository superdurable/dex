package cluster

import (
	"fmt"
	"os"
	"testing"

	"github.com/superdurable/dex/server/internal/integration/testhelpers"
)

// TestMain provisions the per-store schema once and boots the shared 2-node
// cluster used by every TestCluster_* testcase. Sharing the cluster across
// testcases turns 10× ~7s boot+converge into 1× boot, taking the cluster
// sub-package from ~73s down to ~15s.
func TestMain(m *testing.M) {
	uri := testhelpers.TestDBURI()
	if err := testhelpers.EnsureSchemaForPrefix(dbPrefix); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize schema for %q: %v\n", dbPrefix, err)
		os.Exit(1)
	}

	var cleanup func()
	if uri != "" {
		var err error
		sharedNodeA, sharedNodeB, cleanup, err = bootSharedCluster(uri)
		if err != nil {
			// Surface boot errors to individual tests via sharedClusterStartErr
			// so they can t.Fatalf with the underlying cause; do not exit
			// here so `go test -v` still prints per-test names.
			sharedClusterStartErr = err
		}
	}

	code := m.Run()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}
