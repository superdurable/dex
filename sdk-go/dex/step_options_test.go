package dex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// MergeLockingKeys must accumulate BOTH static and dynamic keys into the
// result, and must not mutate its inputs.
func TestMergeLockingKeys_MergesBothAndDoesNotMutateInputs(t *testing.T) {
	a := &LockingKeys{StaticKeyNames: []string{"s1"}, DynamicKeyPrefixes: []string{"da-"}}
	b := &LockingKeys{StaticKeyNames: []string{"s2"}, DynamicKeyPrefixes: []string{"db-"}}

	merged := MergeLockingKeys(a, b)

	assert.ElementsMatch(t, []string{"s1", "s2"}, merged.StaticKeyNames)
	assert.ElementsMatch(t, []string{"da-", "db-"}, merged.DynamicKeyPrefixes,
		"dynamic prefixes from all inputs must be present in the merged result")

	// Inputs must be untouched.
	assert.Equal(t, []string{"da-"}, a.DynamicKeyPrefixes)
	assert.Equal(t, []string{"db-"}, b.DynamicKeyPrefixes)
}
