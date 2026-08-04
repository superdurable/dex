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
