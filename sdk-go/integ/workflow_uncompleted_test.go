// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
	assert.Equal(t, dex.NewWorkflowUncompletedError(runId, dexpb.FAILED, ptr.Any(dexpb.STATE_DECISION_FAILING_WORKFLOW_ERROR_TYPE), wErr.ErrorMessage, nil, dex.GetDefaultObjectEncoder()), wErr)

	out, err2 := client.GetComplexWorkflowResults(context.Background(), wfId, "")
	assert.Nil(t, out)
	assert.Equal(t, err, err2)
	assert.NotNil(t, wErr.ErrorMessage)
	assert.True(t, strings.Contains(*wErr.ErrorMessage, "a failing message"), "must contain failing message")
	assert.True(t, strings.Contains(err.Error(), "a failing message"))
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
