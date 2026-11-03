// Copyright (c) 2026 Super Durable, Inc.
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
	"testing"

	"github.com/stretchr/testify/require"
	rpccancelingsteps "github.com/superdurable/dex/examples/go/primitives/rpc-canceling-steps"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestStopReportCancelsRunningGeneration(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "rpc-canceling-steps")
	_, err := integClient.StartFlow(ctx, registry.ReportGeneration, flowID, "q3", dex.StartFlowOptions{})
	require.NoError(t, err)

	var firstSection int
	_, err = integClient.ReadStream(ctx, flowID, rpccancelingsteps.ReportProgress, "", &firstSection)
	require.NoError(t, err)
	require.Equal(t, 1, firstSection)

	var isStopping bool
	require.NoError(t, integClient.InvokeRPC(ctx, flowID, registry.ReportGeneration.StopReport, nil, &isStopping))
	require.True(t, isStopping)

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output string
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, rpccancelingsteps.ReportStopped, output)
}
