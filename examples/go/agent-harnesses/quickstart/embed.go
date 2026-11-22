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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.

package quickstart

import (
	"context"
	"errors"
	"fmt"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// AskAgent is the smallest host for a durable agent: one Worker, one Client, one turn.
//
// It starts a session, sends one message, and returns the session after the turn.
func AskAgent(
	ctx context.Context,
	sessionFlow *dexagents.AgentSessionFlow,
	sessionID string,
	message string,
	flowServiceAddress string,
	blobCacheDir string,
) (dexagents.AgentSnapshot, error) {
	registry, err := dex.NewRegistry([]dex.Flow{sessionFlow})
	if err != nil {
		return dexagents.AgentSnapshot{}, err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: blobCacheDir, MaxBytes: 1 << 30})
	if err != nil {
		return dexagents.AgentSnapshot{}, err
	}
	defer cache.Close()
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: flowServiceAddress})
	if err != nil {
		return dexagents.AgentSnapshot{}, err
	}
	defer client.Close()
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: flowServiceAddress})
	if err != nil {
		return dexagents.AgentSnapshot{}, err
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	defer func() {
		_ = worker.Stop(context.Background())
		<-workerResult
	}()
	registrationContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(registrationContext); err != nil {
		return dexagents.AgentSnapshot{}, err
	}

	sessions := dexagents.NewAgentSessionClient(client, sessionFlow)
	if _, err := sessions.StartSession(ctx, sessionID, &message, dex.StartFlowOptions{}); err != nil {
		return dexagents.AgentSnapshot{}, err
	}
	for {
		snapshot, err := sessions.GetAgentSnapshot(ctx, sessionID)
		if err != nil {
			return dexagents.AgentSnapshot{}, err
		}
		if state := snapshot.State; state != nil {
			if state.Status == dexagents.AgentSessionStatusFailed && state.LastTurnError != nil {
				return dexagents.AgentSnapshot{}, fmt.Errorf("the session failed: %s", *state.LastTurnError)
			}
			if state.Status == dexagents.AgentSessionStatusFailed {
				return dexagents.AgentSnapshot{}, errors.New("the session failed")
			}
			if state.TurnCount >= 1 && state.Status == dexagents.AgentSessionStatusWaitingForUserMessage {
				return snapshot, nil
			}
		}
		select {
		case <-ctx.Done():
			return dexagents.AgentSnapshot{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
