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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type timerFlow struct {
	emptyFlowSchema
}

func (timerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(timerStep{})}
}

type timerStep struct {
	dex.DefaultStepOptions
}

func (timerStep) WaitFor(
	_ dex.Context,
	seconds int,
) (dex.Wait, error) {
	return dex.AllOf(dex.Timer(time.Duration(seconds) * time.Second)), nil
}

func (timerStep) Execute(
	ctx dex.Context,
	seconds int,
) (dex.StepDecision, error) {
	if !ctx.HasTimerFired() || !ctx.HasTimerFiredByIndex(0) {
		return dex.StepDecision{}, fmt.Errorf("natural timer was not reported as fired")
	}
	return dex.GracefulComplete(seconds + 1), nil
}

func TestTimerFlow(t *testing.T) {
	flowID := newFlowID(t, "timer")
	startedAt := time.Now()
	_, err := integClient.StartFlow(
		integrationContext(t),
		timerFlow{},
		flowID,
		2,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, true)
	elapsed := time.Since(startedAt)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.GreaterOrEqual(t, elapsed, 1500*time.Millisecond)
	require.Less(t, elapsed, 8*time.Second)
	require.Len(t, result.Completions, 1)
	var output int
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, 3, output)
}
