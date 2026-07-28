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

import (
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type StateOptions struct {
	// apply for both waitUntil and execute API
	DataAttributesLoadingPolicy   *dexpb.PersistenceLoadingPolicy
	SearchAttributesLoadingPolicy *dexpb.PersistenceLoadingPolicy
	// below are wait_until API specific options:
	WaitUntilApiTimeoutSeconds                *int32
	WaitUntilApiRetryPolicy                   *dexpb.RetryPolicy
	WaitUntilApiFailurePolicy                 *dexpb.WaitUntilApiFailurePolicy
	WaitUntilApiDataAttributesLoadingPolicy   *dexpb.PersistenceLoadingPolicy
	WaitUntilApiSearchAttributesLoadingPolicy *dexpb.PersistenceLoadingPolicy
	// below are execute API specific options:
	ExecuteApiTimeoutSeconds                *int32
	ExecuteApiRetryPolicy                   *dexpb.RetryPolicy
	ExecuteApiFailureProceedState           WorkflowState
	ExecuteApiDataAttributesLoadingPolicy   *dexpb.PersistenceLoadingPolicy
	ExecuteApiSearchAttributesLoadingPolicy *dexpb.PersistenceLoadingPolicy
}
