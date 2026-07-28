// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/workflows/microservices"
	"github.com/superdurable/dex/examples/go/workflows/service"
)

func TestMicroserviceOrchestrationWorkflow(t *testing.T) {
	wf := microservices.NewMicroserviceOrchestrationWorkflow(service.NewMyService())
	wfID := fmt.Sprintf("examples/go-microservice-%d", time.Now().UnixNano())

	runID, err := client.StartWorkflow(context.Background(), wf, wfID, 60, "initial-data", nil)
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	rpcWorkflow := microservices.OrchestrationWorkflow{}
	require.Eventually(t, func() bool {
		var current string
		rpcErr := client.InvokeRPC(context.Background(), wfID, "", rpcWorkflow.Swap, "initial-data", &current)
		return rpcErr == nil && current == "initial-data"
	}, 15*time.Second, 200*time.Millisecond)

	var swapped string
	err = client.InvokeRPC(context.Background(), wfID, "", rpcWorkflow.Swap, "updated-data", &swapped)
	require.NoError(t, err)
	require.Equal(t, "initial-data", swapped)

	err = client.SignalWorkflow(context.Background(), wf, wfID, "", microservices.SignalChannelReady, nil)
	require.NoError(t, err)

	var ignored interface{}
	err = client.GetSimpleWorkflowResult(context.Background(), wfID, "", &ignored)
	require.NoError(t, err)
}
