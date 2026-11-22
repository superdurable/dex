// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A snapshot with no state, or one that already stored the failure, gets nothing from the Flow's close.
func TestOnlyAStoredStateThatIsNotFailedCanHideASessionFailure(t *testing.T) {
	require.False(t, mayHideSessionFailure(AgentSnapshot{}))
	require.False(t, mayHideSessionFailure(AgentSnapshot{State: &AgentState{Status: AgentSessionStatusFailed}}))
	require.True(t, mayHideSessionFailure(AgentSnapshot{State: &AgentState{Status: AgentSessionStatusWaitingForUserMessage}}))
}
