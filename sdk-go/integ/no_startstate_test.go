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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNoStartStateWorkflow(t *testing.T) {
	wfId := "TestNoStartStateWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	wf := noStartStateWorkflow{}

	runId, err := client.StartWorkflow(context.Background(), wf, wfId, 10, 1, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	var rpcOutput int
	err = client.InvokeRPC(context.Background(), wfId, "", wf.TestRPC, 1, &rpcOutput)
	assert.Nil(t, err)
	assert.Equal(t, 2, rpcOutput)

	time.Sleep(time.Second * 2)
	info, err := client.DescribeWorkflow(context.Background(), wfId, "")
	assert.Nil(t, err)
	assert.Equal(t, dexpb.COMPLETED, info.Status)
}
