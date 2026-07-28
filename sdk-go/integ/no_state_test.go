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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNoStateWorkflow(t *testing.T) {
	wfId := "TestNoStateWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	wf := noStateWorkflow{}

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

	err = client.StopWorkflow(context.Background(), wfId, "", &dex.WorkflowStopOptions{
		StopType: dexpb.FAIL,
		Reason:   "test",
	})
	assert.Nil(t, err)
	time.Sleep(time.Second * 2)
	info, err = client.DescribeWorkflow(context.Background(), wfId, "")
	assert.Nil(t, err)
	assert.Equal(t, dexpb.FAILED, info.Status)
}
