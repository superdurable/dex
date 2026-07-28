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

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

type WorkflowOptions struct {
	WorkflowIdReusePolicy     *dexpb.IDReusePolicy
	WorkflowCronSchedule      *string
	WorkflowStartDelaySeconds *int32
	WorkflowRetryPolicy       *dexpb.WorkflowRetryPolicy
	// InitialSearchAttributes set the initial search attributes to start a workflow
	// key is search attribute key, value much match with PersistenceSchema of the workflow definition
	// For dexpb.DATETIME , the value can be either time.Time or a string value in format of DateTimeFormat
	InitialSearchAttributes map[string]interface{}
}
