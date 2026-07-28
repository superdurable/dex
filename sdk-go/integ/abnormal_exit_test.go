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
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/stretchr/testify/assert"
)

func TestAbnormalExitWorkflow(t *testing.T) {
	wfId := "TestAbnormalExitWorkflow" + strconv.Itoa(int(time.Now().Unix()))

	opt := dex.WorkflowOptions{
		WorkflowIdReusePolicy: ptr.Any(dexpb.ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY),
	}

	runId, err := client.StartWorkflow(context.Background(), &abnormalExitWorkflow{}, wfId, 10, nil, &opt)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)
	wErr, ok := dex.AsWorkflowUncompletedError(err)
	assert.True(t, ok)
	assert.True(t, strings.Contains(*wErr.ErrorMessage, "abnormal exit state"))
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.FAILED, ptr.Any(dexpb.STATE_API_FAIL_ERROR_TYPE), wErr.ErrorMessage, wErr.StateResults, dex.GetDefaultObjectEncoder()), wErr)

	// Starting a workflow with the same ID should be allowed since the previous failed abnormally
	_, err = client.StartWorkflow(context.Background(), &basicWorkflow{}, wfId, 10, 1, &opt)
	assert.False(t, dex.IsWorkflowAlreadyStartedError(err))

	var output int
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Nil(t, err)
	assert.Equal(t, 3, output)
}
