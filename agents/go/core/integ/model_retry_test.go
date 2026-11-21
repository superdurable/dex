// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/sdk-go/dex"
)

// outageModel fails every model call with status until outage after the first call, then answers.
type outageModel struct {
	mutex     sync.Mutex
	callTimes []time.Time
}

func (model *outageModel) respond(status int, outage time.Duration) agentstest.ScriptedModelResponder {
	return func(builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
		model.mutex.Lock()
		defer model.mutex.Unlock()
		model.callTimes = append(model.callTimes, time.Now())
		if time.Since(model.callTimes[0]) < outage {
			return agentstest.ScriptedModelReply{}, &openai.Error{
				StatusCode: status, Response: &http.Response{StatusCode: status, Header: http.Header{}},
			}
		}
		return agentstest.ScriptedModelReply{Text: "the model API is back"}, nil
	}
}

func (model *outageModel) calls() []time.Time {
	model.mutex.Lock()
	defer model.mutex.Unlock()
	return append([]time.Time(nil), model.callTimes...)
}

func startOutageSession(t *testing.T, flowType string, status int, outage time.Duration) (dexagents.AgentSnapshot, []time.Time) {
	t.Helper()
	stack := newAgentTestStack(t)
	model := &outageModel{}
	newFlow := func() *dexagents.AgentSessionFlow {
		definition, err := builtinloop.NewAgentDefinition("outage", "You answer.",
			agentstest.NewScriptedModelClient(model.respond(status, outage)))
		require.NoError(t, err)
		flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, flowType)
		require.NoError(t, err)
		return flow
	}
	stack.startInProcessWorker([]dex.Flow{newFlow()})
	stack.addClientFlow(newFlow())
	_, err := stack.sessions(flowType).StartSession(context.Background(), "outage", ptr("hello"), dex.StartFlowOptions{})
	require.NoError(t, err)
	return stack.waitForTurnCount(flowType, "outage", 1), model.calls()
}

func TestTwentySecondModelOutageNoLongerFailsTheTurn(t *testing.T) {
	t.Parallel()
	snapshot, calls := startOutageSession(t, "OutageAgent", http.StatusServiceUnavailable, 20*time.Second)

	require.Nil(t, snapshot.State.LastTurnError)
	require.GreaterOrEqual(t, calls[len(calls)-1].Sub(calls[0]), 20*time.Second)
	// Waits of 0.5-1, 1-2, 2-4, 4-8, and 8-16 seconds reach 20 seconds within six or seven calls.
	require.GreaterOrEqual(t, len(calls), 5)
	require.LessOrEqual(t, len(calls), 9)
}

func TestBadRequestFailsTheTurnWithoutARetry(t *testing.T) {
	t.Parallel()
	snapshot, calls := startOutageSession(t, "BadRequestAgent", http.StatusBadRequest, time.Hour)

	require.NotNil(t, snapshot.State.LastTurnError)
	require.Contains(t, *snapshot.State.LastTurnError, "400")
	require.Len(t, calls, 1)
}

func TestErrorThatIsNotAModelAPIErrorKeepsThreeAttempts(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	var mutex sync.Mutex
	calls := 0
	newFlow := func() *dexagents.AgentSessionFlow {
		definition, err := builtinloop.NewAgentDefinition("bug", "You answer.",
			agentstest.NewScriptedModelClient(func(builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
				mutex.Lock()
				defer mutex.Unlock()
				calls++
				return agentstest.ScriptedModelReply{}, errors.New("the runner has a bug")
			}))
		require.NoError(t, err)
		flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, "RunnerBugAgent")
		require.NoError(t, err)
		return flow
	}
	stack.startInProcessWorker([]dex.Flow{newFlow()})
	stack.addClientFlow(newFlow())
	_, err := stack.sessions("RunnerBugAgent").StartSession(context.Background(), "bug", ptr("hello"), dex.StartFlowOptions{})
	require.NoError(t, err)

	snapshot := stack.waitForTurnCount("RunnerBugAgent", "bug", 1)

	require.NotNil(t, snapshot.State.LastTurnError)
	require.Contains(t, *snapshot.State.LastTurnError, "the runner has a bug")
	mutex.Lock()
	defer mutex.Unlock()
	require.Equal(t, 3, calls)
}
