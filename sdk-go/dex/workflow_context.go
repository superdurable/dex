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

//go:generate mockgen -source=./workflow_context.go -package=dextest -destination=../dextest/workflow_context.go

import "context"

type WorkflowContext interface {
	context.Context
	GetWorkflowId() string
	GetWorkflowStartTimestampSeconds() int64
	GetStateExecutionId() string
	GetWorkflowRunId() string
	// GetFirstAttemptTimestampSeconds returns the start time of the first attempt of the API call. It's from ScheduledTimestamp of Cadence/Temporal activity.GetInfo
	// require server version 1.2.2+, return 0 if server version is lower
	GetFirstAttemptTimestampSeconds() int64
	// GetAttempt returns an attempt number, which starts from 1, and increased by 1 for every retry if retry policy is specified. It's from Attempt of Cadence/Temporal activity.GetInfo
	// require server version 1.2.2+, return 0 if server version is lower
	GetAttempt() int
}
