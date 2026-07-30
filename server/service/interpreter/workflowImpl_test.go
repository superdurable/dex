// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package interpreter

import (
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

func TestProcessTransientStepExecutionFailsWithoutProceeding(t *testing.T) {
	controller := gomock.NewController(t)
	provider := interfaces.NewMockWorkflowProvider(controller)
	ctx := interfaces.NewUnifiedContext("workflow")
	executeError := errors.New("transient execute failed")
	flowConfiger := interpreterconfig.NewFlowConfiger(&dexpb.FlowConfig{})
	counter := NewStepExecutionCounter(
		ctx,
		provider,
		flowConfiger,
		cont.NewContinueAsCounter(flowConfiger, ctx, provider),
	)
	step := &dexpb.StepMovement{
		StepType:                        "transient",
		StepOptions:                     &dexpb.StepOptions{SkipWaitFor: true},
		FromStepExecutionIdInternalOnly: "source-1",
	}

	provider.EXPECT().GetWorkflowInfo(ctx).Return(interfaces.WorkflowInfo{
		WorkflowExecution: interfaces.WorkflowExecution{ID: "flow", RunID: "run"},
		WorkflowStartTime: time.Unix(100, 0),
		FirstRunID:        "first-run",
	})
	provider.EXPECT().WithActivityOptions(ctx, gomock.Any()).Return(ctx)
	provider.EXPECT().
		ExecuteActivity(
			gomock.Any(),
			dexpb.StepDurability_STEP_DURABILITY_SYNC,
			ctx,
			gomock.Any(),
			gomock.Any(),
		).
		DoAndReturn(func(
			_ interface{},
			_ dexpb.StepDurability,
			_ interfaces.UnifiedContext,
			_ interface{},
			args ...interface{},
		) error {
			require.Len(t, args, 1)
			input := args[0].(*dexpb.InvokeExecuteMethodActivityInput)
			require.True(t, input.GetIsTransientStep())
			require.Equal(t, "transient-1", input.GetRequest().GetContext().GetStepExecutionId())
			require.Equal(t, "source-1", input.GetRequest().GetContext().GetFromStepExecutionId())
			return executeError
		})

	interpreter := &Interpreter{activities: &Activities{}}
	status, err := interpreter.processTransientStepExecution(
		ctx,
		provider,
		service.BasicInfo{FlowType: "flow-type"},
		step,
		NewPersistenceManager(provider, nil),
		NewChannelStore(),
		&ContinueAsNewer{StepExecutionToResumeMap: map[string]*dexpb.StepExecutionResumeInfo{}},
		counter,
		flowConfiger,
	)

	require.ErrorIs(t, err, executeError)
	require.Equal(t, service.StepExecutionStatusFailedNoProceed, status)
	require.Equal(t, int32(1), counter.GetTotalCurrentlyExecutingCount())
	require.Empty(t, counter.Dump().GetStepTypeStartedCount()["fallback"])
}
