// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
