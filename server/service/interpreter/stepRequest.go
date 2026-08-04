// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
