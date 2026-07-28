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

import "github.com/superdurable/dex/gen/dexpb"

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

func (srq *StepRequestQueue) TakeAll() []StepRequest {
	// Copy the whole slice pointer.
	res := srq.queue
	// Reset because each iteration processes the current queue.
	srq.queue = nil
	return res
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

func (srq *StepRequestQueue) AddSingleStepStartRequest(
	stepType string,
	input *dexpb.Value,
	options *dexpb.StepOptions,
) {
	srq.queue = append(srq.queue, NewStepStartRequest(&dexpb.StepMovement{
		StepType:    stepType,
		StepInput:   input,
		StepOptions: options,
	}))
}
