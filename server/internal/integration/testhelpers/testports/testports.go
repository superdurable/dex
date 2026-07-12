// Package testports holds the disjoint memberlist port reservations used by
// every test package that spins up real memberlist instances. Memberlist
// requires a fixed UDP+TCP port (cannot use :0); centralizing the ranges
// here lets `go test ./...` run those packages in parallel without TCP/UDP
// collisions and gives one place to grep when adding a new range.
//
// This package intentionally has zero imports outside the standard library
// so it can be pulled in by lower-level packages (server/internal/cluster)
// without creating an import cycle through the heavier helpers in
// server/internal/integration/testhelpers.
//
// Add a new constant when introducing a package that needs fixed ports;
// keep .cursor/rules/test-database-isolation.mdc in sync with this list.
package testports

const (
	// InternalCluster reserves 37946..37999 for
	// server/internal/cluster Membership unit tests
	// (see membership_min_members_test.go).
	InternalCluster = 37946

	// IntegrationCluster reserves 17946..17999 for the shared 2-node
	// cluster booted by server/internal/integration/cluster TestMain.
	// Offsets 0..3 cover shard + tasklist memberlist on each node; offsets
	// 4..7 cover each node's run + matching gRPC listeners (concrete ports
	// are required because local clients dial Cluster.AdvertiseGRPCAddress).
	// Remaining slots are reserved for future expansion.
	IntegrationCluster = 17946
)
