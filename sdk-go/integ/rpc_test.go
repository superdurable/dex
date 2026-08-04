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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRPCWorkflow(t *testing.T) {
	wfId := "TestRPCWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	wf := rpcWorkflow{}

	runId, err := client.StartWorkflow(context.Background(), wf, wfId, 10, 1, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	time.Sleep(time.Second)
	info, err := client.DescribeWorkflow(context.Background(), wfId, "")
	assert.Nil(t, err)
	assert.Equal(t, dexpb.RUNNING, info.Status)

	err = client.InvokeRPC(context.Background(), wfId, "", wf.TestErrorRPC, 1, nil)
	assert.NotNil(t, err)
	assert.True(t, dex.IsRPCError(err))
	rpcErr, _ := err.(*dex.ApiError)
	assert.Equal(t, "worker API error, status:501, errorType:test-error-type", rpcErr.Response.GetDetail())

	// Test unregister client
	unregClient := dex.NewUnregisteredClient(nil)
	err = unregClient.InvokeRPCByName(context.Background(), wfId, "", "TestErrorRPC", 1, nil, nil)
	assert.NotNil(t, err)

	var rpcOutput int
	err = client.InvokeRPC(context.Background(), wfId, "", wf.TestRPC, 1, &rpcOutput)
	assert.Nil(t, err)
	assert.Equal(t, 2, rpcOutput)

	var output int
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Nil(t, err)
	assert.Equal(t, 3, output)
}
