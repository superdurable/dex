package ids_test

import (
	"testing"

	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateBlobID(t *testing.T) {
	// Valid UUID round-trips to a non-zero BlobID.
	minted := ids.NewBlobID()
	got, err := ids.ValidateBlobID(minted.String())
	require.NoError(t, err)
	assert.Equal(t, minted, got)
	assert.False(t, got.IsZero())

	// Empty and malformed client input are rejected (no panic).
	for _, bad := range []string{"", "not-a-uuid", "1234"} {
		_, err := ids.ValidateBlobID(bad)
		assert.Error(t, err, "input %q should be rejected", bad)
	}
}
