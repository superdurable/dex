// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeterministicBlobObjectIDStableVector(t *testing.T) {
	objectID, err := deterministicBlobObjectID("run-123activity-456", []byte("payload"), 10)
	require.NoError(t, err)
	require.Equal(t, "8d1zvkzfui", objectID)
}

func TestDeterministicBlobObjectIDLengthsAndAlphabet(t *testing.T) {
	for _, objectIDLength := range []int{10, 12, 16, 22, 50} {
		objectID, err := deterministicBlobObjectID("request", []byte("payload"), objectIDLength)
		require.NoError(t, err)
		require.Len(t, objectID, objectIDLength)
		require.True(t, regexp.MustCompile(`^[0-9a-z]+$`).MatchString(objectID))
	}
}

func TestDeterministicBlobObjectIDFramesComponents(t *testing.T) {
	firstID, err := deterministicBlobObjectID("ab", []byte("c"), 10)
	require.NoError(t, err)
	secondID, err := deterministicBlobObjectID("a", []byte("bc"), 10)
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
}

func TestDeterministicBlobObjectIDValuePathUsesContextualFlow(t *testing.T) {
	path, err := ValueObjectPath("flow/one", "260913/0abcde1234")
	require.NoError(t, err)
	require.Equal(t, "260913$Zmxvdy9vbmU/0abcde1234", path)

	for _, locator := range []string{
		"260913/abcdefghi",
		"260913/ABCDEF1234",
		"260913/abcdef1234/extra",
		"261340/abcdef1234",
		"20260913/abcdef1234",
	} {
		_, err := ValueObjectPath("flow", locator)
		require.Error(t, err)
	}
}
