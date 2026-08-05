// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type noStepFlow struct {
	emptyFlowSchema
}

func (noStepFlow) GetSteps() []dex.StepDef {
	return nil
}

func (noStepFlow) Fail(
	dex.Context,
	int,
) (dex.RPCResult[int], error) {
	return dex.RPCResult[int]{}, fmt.Errorf("planned no-step RPC failure")
}

func TestFlowWithoutSteps(t *testing.T) {
	ctx := integrationContext(t)
	flow := noStepFlow{}
	flowID := newFlowID(t, "no-step")
	_, err := integClient.StartFlow(
		ctx,
		flow,
		flowID,
		nil,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	var searchPage dex.SearchFlowsPage
	require.Eventually(t, func() bool {
		searchPage, err = integClient.SearchFlows(
			ctx,
			"FlowType = '"+dex.GetFinalFlowType(flow)+"'",
			100,
			"",
		)
		if err != nil {
			return false
		}
		for _, entry := range searchPage.Flows {
			if entry.FlowID == flowID && entry.Status == dex.FlowRunning {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond, "SearchFlows failed: %v", err)
	var output int
	err = integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Fail,
		1,
		&output,
		dex.InvokeOptions{},
	)
	sdkError := requireDexError(t, err, dex.ErrorWorkerAPI)
	require.NotNil(t, sdkError.OriginalWorkerError)
	require.True(t, strings.Contains(
		sdkError.OriginalWorkerError.Detail,
		"planned no-step RPC failure",
	))
	require.NoError(t, integClient.StopFlow(
		ctx,
		flowID,
		dex.StopOptions{Type: dex.FailFlow, Reason: "test"},
	))
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
}
