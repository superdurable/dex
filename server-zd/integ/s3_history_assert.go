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
	"testing"

	"github.com/stretchr/testify/require"
)

// requireServerZDValueReferences asserts which recorded Worker values server-zd sent as references.
func requireServerZDValueReferences(
	t *testing.T,
	history map[string]any,
	lazyLoading bool,
	offloadedKeys []string,
	inlineKeys []string,
) {
	t.Helper()

	for _, key := range offloadedKeys {
		require.Equal(t, lazyLoading, history[key], key)
	}
	for _, key := range inlineKeys {
		require.Equal(t, false, history[key], key)
	}
}
