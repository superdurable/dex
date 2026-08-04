// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDeterministicBlobUUIDStableVector(t *testing.T) {
	objectID, err := deterministicBlobUUID("run-123activity-456", []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, "d3333fb9-f46d-8815-8359-9892fd394a6e", objectID.String())
}

func TestDeterministicBlobUUIDVersionAndVariant(t *testing.T) {
	objectID, err := deterministicBlobUUID("request", []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, uuid.Version(8), objectID.Version())
	require.Equal(t, uuid.RFC4122, objectID.Variant())
}

func TestDeterministicBlobUUIDFramesComponents(t *testing.T) {
	firstID, err := deterministicBlobUUID("ab", []byte("c"))
	require.NoError(t, err)
	secondID, err := deterministicBlobUUID("a", []byte("bc"))
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
}
