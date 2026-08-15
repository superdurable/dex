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
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
)

type StepRequestQueue struct {
	queue []StepRequest
}

func NewStepRequestQueue() *StepRequestQueue {
	return &StepRequestQueue{}
}

func NewStepRequestQueueWithResumeRequests(
	startReqs []*dexpb.StepMovement,
	resumeReqs []*dexpb.StepExecutionResumeInfo,
) *StepRequestQueue {
	var queue []StepRequest
	for _, request := range startReqs {
		queue = append(queue, NewStepStartRequest(request))
	}

	resumeReqsById := make(map[string]StepRequest, len(resumeReqs))
	for _, request := range resumeReqs {
		stepRequest := NewStepResumeRequest(request)
		stepExecutionId := request.GetStepExecutionId()
		if _, ok := resumeReqsById[stepExecutionId]; ok {
			panic("duplicate step execution ID in resume requests")
		}
		resumeReqsById[stepExecutionId] = stepRequest
	}
	for _, stepExecutionId := range DeterministicKeys(resumeReqsById) {
		queue = append(queue, resumeReqsById[stepExecutionId])
	}
	return &StepRequestQueue{queue: queue}
}

func (srq *StepRequestQueue) IsEmpty() bool {
	return len(srq.queue) == 0
}

// HasUserRequests reports whether user Step work is queued.
func (srq *StepRequestQueue) HasUserRequests() bool {
	for _, request := range srq.queue {
		if request.GetStepType() != service.FlowTimeoutStepType {
			return true
		}
	}
	return false
}

func (srq *StepRequestQueue) TakeAll() []StepRequest {
	// Copy the whole slice pointer.
	res := srq.queue
	// Reset because each iteration processes the current queue.
	srq.queue = nil
	return res
}

func (srq *StepRequestQueue) RemoveMatching(
	matches func(StepRequest) bool,
) []StepRequest {
	retained := make([]StepRequest, 0, len(srq.queue))
	var removed []StepRequest
	for _, request := range srq.queue {
		if matches(request) {
			removed = append(removed, request)
			continue
		}
		retained = append(retained, request)
	}
	srq.queue = retained
	return removed
}

func (srq *StepRequestQueue) GetAllStepStartRequests() []*dexpb.StepMovement {
	var res []*dexpb.StepMovement
	for _, request := range srq.queue {
		if !request.IsResumeRequest() {
			res = append(res, request.GetStepStartRequest())
		}
	}
	return res
}

func (srq *StepRequestQueue) GetAllStepResumeRequests() map[string]*dexpb.StepExecutionResumeInfo {
	res := make(map[string]*dexpb.StepExecutionResumeInfo)
	for _, request := range srq.queue {
		if request.IsResumeRequest() {
			resumeRequest := request.GetStepResumeRequest()
			res[resumeRequest.GetStepExecutionId()] = resumeRequest
		}
	}
	return res
}

func (srq *StepRequestQueue) AddStepStartRequests(reqs []*dexpb.StepMovement) {
	for _, request := range reqs {
		srq.queue = append(srq.queue, NewStepStartRequest(request))
	}
}

func (srq *StepRequestQueue) AddStepResumeRequest(
	resumeInfo *dexpb.StepExecutionResumeInfo,
) {
	srq.queue = append(srq.queue, NewStepResumeRequest(resumeInfo))
}

func (srq *StepRequestQueue) AddSingleStepStartRequest(
	stepType string,
	input *dexpb.Value,
	options *dexpb.StepOptions,
	source string,
	recoveryError *dexpb.RecoveryErrorInfo,
) {
	srq.queue = append(srq.queue, NewStepStartRequest(&dexpb.StepMovement{
		StepType:                        stepType,
		StepInput:                       input,
		StepOptions:                     options,
		FromStepExecutionIdInternalOnly: source,
		RecoveryErrorInternalOnly:       recoveryError,
	}))
}
