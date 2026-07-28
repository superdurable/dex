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

type StepRequest struct {
	stepStartRequest  *dexpb.StepMovement
	stepResumeRequest *dexpb.StepExecutionResumeInfo
}

func NewStepStartRequest(movement *dexpb.StepMovement) StepRequest {
	if movement == nil {
		panic("step start request requires a movement")
	}
	return StepRequest{stepStartRequest: movement}
}

func NewStepResumeRequest(resumeRequest *dexpb.StepExecutionResumeInfo) StepRequest {
	if resumeRequest == nil || resumeRequest.GetStep() == nil {
		panic("step resume request requires resume info and a movement")
	}
	if resumeRequest.GetStepExecutionId() == "" {
		panic("step resume request requires an execution ID")
	}
	return StepRequest{stepResumeRequest: resumeRequest}
}

func (sq StepRequest) GetStepStartRequest() *dexpb.StepMovement {
	if sq.IsResumeRequest() {
		panic("resume request has no start request")
	}
	return sq.stepStartRequest
}

func (sq StepRequest) GetStepResumeRequest() *dexpb.StepExecutionResumeInfo {
	if !sq.IsResumeRequest() {
		panic("start request has no resume request")
	}
	return sq.stepResumeRequest
}

func (sq StepRequest) IsResumeRequest() bool {
	return sq.stepResumeRequest != nil
}

func (sq StepRequest) GetStepMovement() *dexpb.StepMovement {
	if sq.IsResumeRequest() {
		return sq.stepResumeRequest.GetStep()
	}
	if sq.stepStartRequest == nil {
		panic("invalid empty StepRequest")
	}
	return sq.stepStartRequest
}

func (sq StepRequest) GetStepType() string {
	return sq.GetStepMovement().GetStepType()
}
