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
	"fmt"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/stretchr/testify/assert"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWorkflowTimeout(t *testing.T) {
	wfId := "TestWorkflowTimeout" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &signalWorkflow{}, wfId, 1, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)

	wErr, ok := dex.AsWorkflowUncompletedError(err)
	assert.True(t, ok)
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.TIMEOUT, nil, nil, nil, dex.GetDefaultObjectEncoder()), wErr)

	out, err2 := client.GetComplexWorkflowResults(context.Background(), wfId, "")
	assert.Nil(t, out)
	assert.Equal(t, err, err2)

	assert.Equal(t, "workflow is not completed successfully, closedStatus: TIMEOUT, failedErrorType(applies if failed as closedStatus):<nil>, error message:<nil>", err.Error())
}

func TestWorkflowCancel(t *testing.T) {
	wfId := "TestWorkflowCancel" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &signalWorkflow{}, wfId, 10, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	err = client.StopWorkflow(context.Background(), wfId, "", nil)
	assert.Nil(t, err)

	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)

	wErr, ok := dex.AsWorkflowUncompletedError(err)
	assert.True(t, ok)
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.CANCELED, nil, nil, nil, dex.GetDefaultObjectEncoder()), wErr)

	out, err2 := client.GetComplexWorkflowResults(context.Background(), wfId, "")
	assert.Nil(t, out)
	assert.Equal(t, err, err2)

	assert.Equal(t, "workflow is not completed successfully, closedStatus: CANCELED, failedErrorType(applies if failed as closedStatus):<nil>, error message:<nil>", err.Error())
}

func TestForceFailWorkflow(t *testing.T) {
	wfId := "TestForceFailWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &forceFailWorkflow{}, wfId, 10, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)

	wErr, ok := dex.AsWorkflowUncompletedError(err)
	assert.True(t, ok)
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.FAILED, ptr.Any(dexpb.STATE_DECISION_FAILING_WORKFLOW_ERROR_TYPE), nil, wErr.StateResults, dex.GetDefaultObjectEncoder()), wErr)

	out, err2 := client.GetComplexWorkflowResults(context.Background(), wfId, "")
	assert.Nil(t, out)
	assert.Equal(t, err, err2)
	assert.Equal(t, "workflow is not completed successfully, closedStatus: FAILED, failedErrorType(applies if failed as closedStatus):STATE_DECISION_FAILING_WORKFLOW_ERROR_TYPE, error message:<nil>", err.Error())

	var output string
	err = wErr.GetStateResult(0, &output)
	assert.Nil(t, err)
	assert.Equal(t, "a failing message", output)
}

func TestStateApiFailWorkflow(t *testing.T) {
	wfId := "TestStateApiFailWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &stateApiFailWorkflow{}, wfId, 10, nil, &dex.WorkflowOptions{})
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)

	wErr, ok := dex.AsWorkflowUncompletedError(err)
	assert.True(t, ok)
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.FAILED, ptr.Any(dexpb.STATE_API_FAIL_ERROR_TYPE), wErr.ErrorMessage, nil, dex.GetDefaultObjectEncoder()), wErr)

	assert.True(t, strings.Contains(*wErr.ErrorMessage, "test api failing"), "must contain api failing message")

	out, err2 := client.GetComplexWorkflowResults(context.Background(), wfId, "")
	assert.Nil(t, out)
	assert.Equal(t, err, err2)

	assert.True(t, strings.Contains(err.Error(), "workflow is not completed successfully, closedStatus: FAILED, failedErrorType(applies if failed as closedStatus):STATE_API_FAIL_ERROR_TYPE, error message:statusCode: 400, responseBody: {\"error\":\"error message:test api failing"))
}

func TestStateApiTimeoutWorkflow(t *testing.T) {
	wfId := "TestStateApiTimeoutWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &stateApiTimeoutWorkflow{}, wfId, 10, nil, &dex.WorkflowOptions{})
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)

	wErr, ok := dex.AsWorkflowUncompletedError(err)
	assert.True(t, ok)
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.FAILED, ptr.Any(dexpb.STATE_API_FAIL_ERROR_TYPE), wErr.ErrorMessage, nil, dex.GetDefaultObjectEncoder()), wErr)

	fmt.Println(err)

	expectedMsg := "workflow is not completed successfully, closedStatus: FAILED, failedErrorType(applies if failed as closedStatus):STATE_API_FAIL_ERROR_TYPE, error message:activity error "
	assert.True(t, strings.HasPrefix(err.Error(), expectedMsg))

	out, err2 := client.GetComplexWorkflowResults(context.Background(), wfId, "")
	assert.Nil(t, out)
	assert.Equal(t, err, err2)
}

// TODO need to support terminate operation in Stop API first
//func TestWorkflowTerminated(t *testing.T) {
//
//}
