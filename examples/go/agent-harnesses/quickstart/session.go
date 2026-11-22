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

// Package quickstart is the Agent Quick Start in Go: the order support agent on Eino, its host, and one turn.
//
// The Worker and the Client live in one process. The Worker runs the
// session's Steps. The Client starts the session, sends the message, reads
// the session, and answers each approval.
package quickstart

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	pullRegistrationTimeout = 30 * time.Second
	// Tool calls run as parallel Steps, so the Worker needs a slot for each one.
	maxConcurrentTasks = 16
	pollInterval       = 500 * time.Millisecond
)

// ApprovalDecider answers one tool call that waits for approval: true approves it.
type ApprovalDecider func(ctx context.Context, interrupt dexagents.AgentInterrupt) (bool, error)

// QuickStartApp is one Registry, Worker, and Client for one durable agent.
type QuickStartApp struct {
	// Sessions starts, drives, and reads the agent's sessions.
	Sessions *dexagents.AgentSessionClient

	cache        *blobcache.Cache
	worker       *dex.Worker
	workerResult chan error
	client       *dex.Client
}

// StartQuickStartApp starts a Worker for sessionFlow and waits until Dex has registered it.
//
// It returns an error when the blob cache, the Worker, or the Client cannot
// start, or when the Worker does not register within 30 seconds.
func StartQuickStartApp(
	ctx context.Context, sessionFlow *dexagents.AgentSessionFlow, flowServiceAddress string, blobCacheDir string,
) (*QuickStartApp, error) {
	if err := os.MkdirAll(blobCacheDir, 0o755); err != nil {
		return nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: blobCacheDir, MaxBytes: 1 << 30})
	if err != nil {
		return nil, err
	}
	registry, err := dex.NewRegistry([]dex.Flow{sessionFlow})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: flowServiceAddress})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: maxConcurrentTasks},
	})
	if err != nil {
		return nil, errors.Join(err, client.Close(), cache.Close())
	}
	app := &QuickStartApp{
		Sessions: dexagents.NewAgentSessionClient(client, sessionFlow),
		cache:    cache, worker: worker, workerResult: make(chan error, 1), client: client,
	}
	go func() { app.workerResult <- worker.Start() }()
	registrationContext, cancel := context.WithTimeout(ctx, pullRegistrationTimeout)
	defer cancel()
	if err := worker.WaitPullRegistered(registrationContext); err != nil {
		return nil, errors.Join(err, app.Stop(context.Background()))
	}
	return app, nil
}

// Stop lets running Steps finish until ctx ends, then closes the Worker, the Client, and the blob cache.
//
// It returns every error the shutdown met.
func (app *QuickStartApp) Stop(ctx context.Context) error {
	stopErr := app.worker.Stop(ctx)
	var runErr error
	select {
	case runErr = <-app.workerResult:
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	return errors.Join(stopErr, runErr, app.client.Close(), app.cache.Close())
}

// RunTurn sends message, or resumes the active turn when it is nil, and returns the agent's replies.
//
// Each tool call that waits for approval goes to decideApproval once. It
// returns decideApproval's error as it is, an error when the session fails,
// and ctx's error when ctx ends first.
func RunTurn(
	ctx context.Context,
	sessions *dexagents.AgentSessionClient,
	sessionID string,
	message *string,
	decideApproval ApprovalDecider,
) ([]string, error) {
	snapshot, err := sessions.GetAgentSnapshot(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var finishedTurns int64
	if snapshot.State != nil {
		finishedTurns = snapshot.State.TurnCount
	}
	targetTurn := finishedTurns
	if message != nil {
		if _, err := sessions.SendUserMessage(ctx, sessionID, *message); err != nil {
			return nil, err
		}
		targetTurn = finishedTurns + 1
	} else if snapshot.State != nil && snapshot.State.ActiveTurnID != nil {
		targetTurn = finishedTurns + 1
	}
	answered := map[string]bool{}
	for {
		snapshot, err := sessions.GetAgentSnapshot(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		for _, interrupt := range snapshot.PendingInterrupts {
			if answered[interrupt.InterruptID] {
				continue
			}
			answered[interrupt.InterruptID] = true
			isApproved, err := decideApproval(ctx, interrupt)
			if err != nil {
				return nil, err
			}
			if _, err := sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, isApproved, nil); err != nil {
				return nil, err
			}
		}
		state := snapshot.State
		if state != nil && state.Status == dexagents.AgentSessionStatusFailed {
			return nil, fmt.Errorf("the session failed: %s", textOrEmpty(state.LastTurnError))
		}
		if state != nil && state.TurnCount >= targetTurn && state.Status == dexagents.AgentSessionStatusWaitingForUserMessage {
			return assistantReplies(snapshot, fmt.Sprintf("turn-%d", targetTurn)), nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func assistantReplies(snapshot dexagents.AgentSnapshot, turnID string) []string {
	var replies []string
	for _, message := range snapshot.Messages {
		if message.TurnID == turnID && message.Role == dexagents.MessageRoleAssistant && textOrEmpty(message.Text) != "" {
			replies = append(replies, *message.Text)
		}
	}
	return replies
}

func textOrEmpty(text *string) string {
	if text == nil {
		return ""
	}
	return *text
}
