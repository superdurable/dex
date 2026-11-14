// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Command agent-session serves a scripted Dex Agents session Flow and starts one
// two-turn session, so Dex Web's Conversation tab has content. It keeps serving
// as a Worker until it is stopped.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/web/scripts/local-demo/demoworker"
)

// demoAgentFlowType must match flow.name in the session graph that run.sh writes.
const demoAgentFlowType = "DemoAgent"

const (
	turnTimeout       = 60 * time.Second
	turnPollInterval  = 200 * time.Millisecond
	firstUserMessage  = "Hello, who are you?"
	secondUserMessage = "Where is my order?"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	sessionID := flag.String("session-id", fmt.Sprintf("demo-agent-session-%d", time.Now().Unix()),
		"Flow ID of the session to start")
	blobCacheDir := flag.String("blob-cache-dir", "", "BlobCache directory; empty uses a temporary directory")
	flag.Parse()

	flow, err := newDemoAgentSessionFlow()
	if err != nil {
		return err
	}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return demoworker.Serve(ctx, registry, *blobCacheDir, func(ctx context.Context, client *dex.Client) error {
		return startTwoTurnSession(ctx, dexagents.NewAgentSessionClient(client, flow), *sessionID)
	})
}

// newDemoAgentSessionFlow builds a session Flow whose scripted model answers without a network call.
func newDemoAgentSessionFlow() (*dexagents.AgentSessionFlow, error) {
	lookupOrder, err := builtinloop.NewFunctionTool(
		"lookup_order",
		"Look up an order by its ID.",
		json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`),
		dexagents.ToolEffectRead,
		func(_ context.Context, _ json.RawMessage, _ builtinloop.ToolCallContext) (string, error) {
			return "order 42: shipped, arrives Friday", nil
		})
	if err != nil {
		return nil, err
	}
	model := agentstest.NewScriptedModelClient(replyToLatestUserMessage)
	definition, err := builtinloop.NewAgentDefinition("demo", "You are a demo agent.", model,
		builtinloop.WithFunctionTools(lookupOrder))
	if err != nil {
		return nil, err
	}
	return builtinloop.NewBuiltinAgentSessionFlow(definition, demoAgentFlowType)
}

// replyToLatestUserMessage calls lookup_order for an order question and echoes anything else.
func replyToLatestUserMessage(request builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
	userTexts := agentstest.UserTexts(request)
	if len(userTexts) == 0 {
		return agentstest.ScriptedModelReply{Text: "How can I help?"}, nil
	}
	latest := userTexts[len(userTexts)-1]
	if !strings.Contains(latest, "order") {
		return agentstest.ScriptedModelReply{Text: "You said: " + latest}, nil
	}
	toolResults := agentstest.ToolResults(request)
	if len(toolResults) == 0 {
		reasoning := "The user asks about an order; I will look it up."
		return agentstest.ScriptedModelReply{
			Reasoning: &reasoning,
			ToolCalls: []agentstest.ScriptedToolCall{{ToolName: "lookup_order", Arguments: `{"order_id": "42"}`}},
		}, nil
	}
	return agentstest.ScriptedModelReply{Text: "Your order is: " + toolResults[0].Output}, nil
}

// startTwoTurnSession sends the second message only after the first turn finished, so each turn stays separate.
func startTwoTurnSession(ctx context.Context, sessions *dexagents.AgentSessionClient, sessionID string) error {
	firstMessage := firstUserMessage
	runID, err := sessions.StartSession(ctx, sessionID, &firstMessage, dex.StartFlowOptions{})
	if err != nil {
		return fmt.Errorf("start session %s: %w", sessionID, err)
	}
	fmt.Println("SESSION", sessionID, "RUN", runID)
	if err := waitForTurnCount(ctx, sessions, sessionID, 1); err != nil {
		return err
	}
	isQueued, err := sessions.SendUserMessage(ctx, sessionID, secondUserMessage)
	if err != nil {
		return fmt.Errorf("send second message to session %s: %w", sessionID, err)
	}
	if !isQueued {
		return fmt.Errorf("session %s did not queue the second message", sessionID)
	}
	if err := waitForTurnCount(ctx, sessions, sessionID, 2); err != nil {
		return err
	}
	fmt.Println("SESSION", sessionID, "finished two turns")
	return nil
}

func waitForTurnCount(ctx context.Context, sessions *dexagents.AgentSessionClient, sessionID string, turnCount int64) error {
	waitCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	ticker := time.NewTicker(turnPollInterval)
	defer ticker.Stop()
	for {
		snapshot, err := sessions.GetAgentSnapshot(waitCtx, sessionID)
		if err != nil {
			return fmt.Errorf("read snapshot of session %s: %w", sessionID, err)
		}
		if snapshot.State != nil && snapshot.State.TurnCount >= turnCount {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for turn %d of session %s: %w", turnCount, sessionID, waitCtx.Err())
		case <-ticker.C:
		}
	}
}
