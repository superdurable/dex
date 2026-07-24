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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
)

func TestOutputCollectorBindsFirstStartedType(t *testing.T) {
	collector := NewOutputCollector([]string{"step"}, nil)
	collector.RegisterStepStarted("step", "step-1")
	collector.RegisterStepStarted("step", "step-2")

	require.False(t, collector.RecordCompletion(
		"step",
		"step-2",
		&iwfpb.Value{Kind: &iwfpb.Value_StringValue{StringValue: "later"}},
	))
	require.True(t, collector.RecordCompletion("step", "step-1", nil))

	output, completed := collector.GetByStepType("step")
	require.True(t, completed)
	require.NotNil(t, output)
	require.Nil(t, output.GetCompletedStepOutput())
	require.Equal(t, "step-1", output.GetCompletedStepExecutionId())
}

func TestOutputCollectorExplicitExecutionIDIsIndependent(t *testing.T) {
	collector := NewOutputCollector([]string{"step"}, []string{"step-2"})
	collector.RegisterStepStarted("step", "step-1")
	collector.RegisterStepStarted("step", "step-2")

	require.True(t, collector.RecordCompletion("step", "step-2", nil))
	_, typeCompleted := collector.GetByStepType("step")
	require.False(t, typeCompleted)
	explicitOutput, explicitCompleted := collector.GetByStepExecutionId("step-2")
	require.True(t, explicitCompleted)
	require.Nil(t, explicitOutput.GetCompletedStepOutput())

	require.True(t, collector.RecordCompletion("step", "step-1", nil))
	_, typeCompleted = collector.GetByStepType("step")
	require.True(t, typeCompleted)
}

func TestOutputCollectorOrdersRetainedResultsByExecutionNumber(t *testing.T) {
	collector := NewOutputCollector(nil, []string{"beta-10", "alpha-2", "gamma"})
	require.True(t, collector.RecordCompletion("beta", "beta-10", nil))
	require.True(t, collector.RecordCompletion("gamma", "gamma", nil))
	require.True(t, collector.RecordCompletion("alpha", "alpha-2", nil))

	require.Equal(t, []string{"alpha-2", "beta-10", "gamma"}, []string{
		collector.GetAll()[0].GetCompletedStepExecutionId(),
		collector.GetAll()[1].GetCompletedStepExecutionId(),
		collector.GetAll()[2].GetCompletedStepExecutionId(),
	})
}

func TestRebuildOutputCollectorRestoresIndexesAndFirstStartedType(t *testing.T) {
	retained := []*iwfpb.StepCompletionOutput{
		{CompletedStepType: "step", CompletedStepExecutionId: "step-2"},
		{CompletedStepType: "explicit", CompletedStepExecutionId: "explicit-4"},
	}
	inFlight := map[string]*iwfpb.StepExecutionResumeInfo{
		"step-1": {
			StepExecutionId: "step-1",
			Step:            &iwfpb.StepMovement{StepType: "step"},
		},
	}
	collector := RebuildOutputCollector(
		[]string{"step"},
		[]string{"explicit-4", "step-2"},
		retained,
		inFlight,
	)

	_, completed := collector.GetByStepType("step")
	require.False(t, completed)
	explicitOutput, completed := collector.GetByStepExecutionId("explicit-4")
	require.True(t, completed)
	require.Same(t, retained[1], explicitOutput)

	require.True(t, collector.RecordCompletion("step", "step-1", nil))
	typeOutput, completed := collector.GetByStepType("step")
	require.True(t, completed)
	require.Equal(t, "step-1", typeOutput.GetCompletedStepExecutionId())
}
