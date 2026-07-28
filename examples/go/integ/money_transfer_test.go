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
	"github.com/superdurable/iwf/examples/go/workflows/moneytransfer"
	"github.com/superdurable/iwf/examples/go/workflows/service"
)

func TestMoneyTransferWorkflow(t *testing.T) {
	wf := moneytransfer.NewMoneyTransferWorkflow(service.NewMyService())
	wfID := fmt.Sprintf("examples/go-moneytransfer-%d", time.Now().UnixNano())
	input := moneytransfer.TransferRequest{
		FromAccount: "from-ci",
		ToAccount:   "to-ci",
		Amount:      42,
		Notes:       "examples/go integ",
	}

	runID, err := client.StartWorkflow(context.Background(), wf, wfID, 60, input, nil)
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	var result string
	err = client.GetSimpleWorkflowResult(context.Background(), wfID, "", &result)
	require.NoError(t, err)
	require.Contains(t, result, "transfer is done")
	require.Contains(t, result, "from-ci")
	require.Contains(t, result, "to-ci")
}
