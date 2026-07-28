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

package integ

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/stretchr/testify/assert"
)

func TestBasicWorkflow(t *testing.T) {
	wfId := "TestBasicWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &basicWorkflow{}, wfId, 10, 1, &dex.WorkflowOptions{
		WorkflowIdReusePolicy: ptr.Any(dexpb.DISALLOW_REUSE),
		WorkflowRetryPolicy: &dexpb.WorkflowRetryPolicy{
			InitialIntervalSeconds: dexpb.PtrInt32(10),
			MaximumAttempts:        dexpb.PtrInt32(3),
			MaximumIntervalSeconds: dexpb.PtrInt32(100),
			BackoffCoefficient:     dexpb.PtrFloat32(3),
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	// start the same workflowId again will fail
	_, err = client.StartWorkflow(context.Background(), &basicWorkflow{}, wfId, 10, nil, nil)
	assert.True(t, dex.IsWorkflowAlreadyStartedError(err))

	var output int
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Nil(t, err)
	assert.Equal(t, 3, output)

	err = client.GetSimpleWorkflowResult(context.Background(), "a wrong workflowId", "", &output)
	assert.True(t, dex.IsWorkflowNotExistsError(err))
}

func TestProceedOnStateStartFailWorkflow(t *testing.T) {
	wfId := "TestProceedOnStateStartFailWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &proceedOnStateStartFailWorkflow{}, wfId, 10, "input", &dex.WorkflowOptions{})
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	_, err = client.StartWorkflow(context.Background(), &basicWorkflow{}, wfId, 10, nil, nil)
	assert.True(t, dex.IsWorkflowAlreadyStartedError(err))

	var output string
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Equal(t, "input_state1_start_state1_decide_state2_start_state2_decide", output)
	assert.Nil(t, err)
}
