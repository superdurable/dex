// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import "context"

type workflowContextImpl struct {
	context.Context
	workflowId                    string
	workflowRunId                 string
	stateExecutionId              string
	workflowStartTimestampSeconds int64
	attempt                       int
	firstAttemptTimestampSeconds  int64
}

func newWorkflowContext(
	ctx context.Context, workflowId string, workflowRunId string, stateExecutionId string, workflowStartTimestampSeconds int64,
	attempt int, firstAttemptTimestampSeconds int64,
) WorkflowContext {
	return &workflowContextImpl{
		Context:                       ctx,
		workflowId:                    workflowId,
		workflowRunId:                 workflowRunId,
		stateExecutionId:              stateExecutionId,
		workflowStartTimestampSeconds: workflowStartTimestampSeconds,
		attempt:                       attempt,
		firstAttemptTimestampSeconds:  firstAttemptTimestampSeconds,
	}
}

func (w workflowContextImpl) GetWorkflowId() string {
	return w.workflowId
}

func (w workflowContextImpl) GetWorkflowStartTimestampSeconds() int64 {
	return w.workflowStartTimestampSeconds
}

func (w workflowContextImpl) GetStateExecutionId() string {
	return w.stateExecutionId
}

func (w workflowContextImpl) GetWorkflowRunId() string {
	return w.workflowRunId
}

func (w workflowContextImpl) GetFirstAttemptTimestampSeconds() int64 {
	return w.firstAttemptTimestampSeconds
}

func (w workflowContextImpl) GetAttempt() int {
	return w.attempt
}
