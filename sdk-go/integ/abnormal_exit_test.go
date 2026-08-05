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

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type abnormalExitFlow struct {
	emptyFlowSchema
}

func (abnormalExitFlow) GetFlowType() string {
	return "go-sdk-abnormal-exit"
}

func (abnormalExitFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(abnormalExitStep{})}
}

type abnormalExitStep struct {
	dex.NoWaitFor[struct{}]
}

func (abnormalExitStep) GetStepType() string {
	return "fail"
}

func (abnormalExitStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1},
	}
}

func (abnormalExitStep) Execute(dex.Context, struct{}) (dex.StepDecision, error) {
	return dex.StepDecision{}, fmt.Errorf("abnormal exit step")
}

func TestAbnormalExitFlow(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "abnormal")
	runID, err := integClient.StartFlow(
		ctx,
		abnormalExitFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{IDReusePolicy: dex.IDReuseAllowIfPreviousFailed},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runID)
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorWorkerMethod, result.ErrorType)
	require.True(t, strings.Contains(result.ErrorMessage, "abnormal exit step"))

	newRunID, err := integClient.StartFlow(
		ctx,
		basicFlow{},
		flowID,
		1,
		dex.StartFlowOptions{IDReusePolicy: dex.IDReuseAllowIfPreviousFailed},
	)
	require.NoError(t, err)
	require.NotEqual(t, runID, newRunID)
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, flowID, false).Status)
}
