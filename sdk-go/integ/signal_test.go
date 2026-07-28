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
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/stretchr/testify/assert"
)

func TestSignalWorkflow(t *testing.T) {
	wfId := "TestSignalWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &signalWorkflow{}, wfId, 10, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)
	err = client.SignalWorkflow(context.Background(), &signalWorkflow{}, wfId, "", testChannelName2, 10)
	assert.Nil(t, err)

	// wait for timer to be ready to be skipped
	time.Sleep(time.Second)
	err = client.SignalWorkflow(context.Background(), &signalWorkflow{}, wfId, "", testChannelName1, 100)
	assert.Nil(t, err)

	err = client.SkipTimerByCommandIndex(context.Background(), wfId, "", signalWorkflowState2{}, 1, 0)
	assert.Nil(t, err)

	var output int
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Nil(t, err)
	assert.Equal(t, 100, output)

	err = client.SignalWorkflow(context.Background(), &signalWorkflow{}, "a wrong workflowId", "", testChannelName1, 100)
	assert.True(t, dex.IsWorkflowNotExistsError(err))
}

func TestSignalWorkflowWithUntypedClient(t *testing.T) {
	unregisteredClient := dex.NewUnregisteredClient(nil)

	wfType := dex.GetFinalWorkflowType(&signalWorkflow{})
	wfId := "TestSignalWorkflowWithUntypedClient" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := unregisteredClient.StartWorkflow(context.Background(), wfType, dex.GetFinalWorkflowStateId(signalWorkflowState1{}), wfId, 10, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)
	err = unregisteredClient.SignalWorkflow(context.Background(), wfId, "", testChannelName2, 10)
	assert.Nil(t, err)

	// wait for timer to be ready to be skipped
	time.Sleep(time.Second)
	err = unregisteredClient.SignalWorkflow(context.Background(), wfId, "", testChannelName1, 100)
	assert.Nil(t, err)

	err = unregisteredClient.SkipTimerByCommandIndex(context.Background(), wfId, "", dex.GetFinalWorkflowStateId(signalWorkflowState2{}), 1, 0)
	assert.Nil(t, err)

	var output int
	err = unregisteredClient.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Nil(t, err)
	assert.Equal(t, 100, output)
}
