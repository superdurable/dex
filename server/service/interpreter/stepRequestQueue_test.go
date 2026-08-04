// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestNewStepRequestQueueWithResumeRequestsUsesStableResumeOrder(t *testing.T) {
	start := &dexpb.StepMovement{StepType: "start"}
	resumeA := &dexpb.StepExecutionResumeInfo{
		StepExecutionId: "resume-a-1",
		Step:            &dexpb.StepMovement{StepType: "resume-a"},
	}
	resumeB := &dexpb.StepExecutionResumeInfo{
		StepExecutionId: "resume-b-1",
		Step:            &dexpb.StepMovement{StepType: "resume-b"},
	}

	queue := NewStepRequestQueueWithResumeRequests(
		[]*dexpb.StepMovement{start},
		[]*dexpb.StepExecutionResumeInfo{resumeB, resumeA},
	)
	requests := queue.TakeAll()
	require.Equal(t, []string{"start", "resume-a", "resume-b"}, []string{
		requests[0].GetStepType(),
		requests[1].GetStepType(),
		requests[2].GetStepType(),
	})
	require.Same(t, start, requests[0].GetStepStartRequest())
	require.Same(t, resumeA, requests[1].GetStepResumeRequest())
	require.True(t, queue.IsEmpty())
}

func TestStepRequestQueueDumpAndOwnership(t *testing.T) {
	start := &dexpb.StepMovement{StepType: "start"}
	resume := &dexpb.StepExecutionResumeInfo{
		StepExecutionId: "resume-1",
		Step:            &dexpb.StepMovement{StepType: "resume"},
	}
	queue := NewStepRequestQueueWithResumeRequests(
		nil,
		[]*dexpb.StepExecutionResumeInfo{resume},
	)

	queue.AddStepStartRequests([]*dexpb.StepMovement{start})
	queue.AddSingleStepStartRequest(
		"single",
		&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "input"}},
		&dexpb.StepOptions{SkipWaitFor: true},
		"source",
	)

	starts := queue.GetAllStepStartRequests()
	require.Len(t, starts, 2)
	require.Same(t, start, starts[0])
	require.Equal(t, "single", starts[1].GetStepType())
	require.Equal(t, "source", starts[1].GetFromStepExecutionIdInternalOnly())
	require.Same(t, resume, queue.GetAllStepResumeRequests()["resume-1"])
}

func TestStepRequestRejectsInvalidResumeInfo(t *testing.T) {
	require.Panics(t, func() {
		NewStepResumeRequest(&dexpb.StepExecutionResumeInfo{})
	})
	require.Panics(t, func() {
		resume := &dexpb.StepExecutionResumeInfo{
			StepExecutionId: "duplicate-id",
			Step:            &dexpb.StepMovement{StepType: "step"},
		}
		NewStepRequestQueueWithResumeRequests(
			nil,
			[]*dexpb.StepExecutionResumeInfo{resume, resume},
		)
	})
}
