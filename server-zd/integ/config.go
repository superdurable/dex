// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
)

type serverZDTestConfig struct {
	// Default zero keeps server defaults; positive values set the encoded JSON object reference boundary.
	ObjectPayloadThresholdBytes int
	// Default empty disables Attribute synchronization; configured stores require PostgreSQL.
	AttributeStore testconfig.AttributeStoreConfig
	// Default empty disables Streams in this harness.
	StreamStore testconfig.StreamStoreConfig
	// Default zero uses a 12-second client wait cap and the server default.
	MaxWaitSeconds int64
	// Default zero keeps the server message limit; test clients allow 32 MiB.
	GrpcMaxMessageBytes int
	// Default nil leaves the public API unauthenticated.
	Auth *testconfig.AuthConfig
	// Default nil keeps server pull-dispatch budgets.
	Matching *testconfig.MatchingConfig
	// Default empty sends no bearer token to API or pull Worker connections.
	Credential string
	// Default nil keeps server hydration defaults; true sends large values as references.
	LazyLoading *bool
	// Default zero keeps server limits; positive values cap both per-node and per-Flow waiters.
	ServerZDMaxLongPollWaiters int
}
