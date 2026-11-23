// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// A source tool's stub body never runs a source call: reached at all, it stops the run like the gate.
func TestSourceToolStubBodyRaisesTheGateInterrupt(t *testing.T) {
	stub, err := newSourceToolStub(dexagents.SourceToolDefinition{Name: "ledger_post_note"})
	require.NoError(t, err)
	output, err := stub.InvokableRun(context.Background(), `{}`)
	require.Empty(t, output)
	_, isInterrupt := compose.IsInterruptRerunError(err)
	require.True(t, isInterrupt, "error %v", err)
}
