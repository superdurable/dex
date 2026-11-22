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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS. They read Flow
// history with dexcli (DEXCLI_PATH, DEXCLI, or PATH). Without a server they
// fail, unless DEX_EXAMPLES_ALLOW_SKIP=1.
package quickstart_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/sdk-go/dex"

	"github.com/superdurable/dex/examples/go/agent-harnesses/demomodel"
	"github.com/superdurable/dex/examples/go/agent-harnesses/orderdesk"
	"github.com/superdurable/dex/examples/go/agent-harnesses/quickstart"
)

const turnTimeout = 90 * time.Second

func TestEinoQuickStartLooksUpAnOrder(t *testing.T) {
	app, _ := startQuickStartApp(t)
	sessionID := startSession(t, app)

	replies := runTurn(t, app, sessionID, "Look up order 1001", refuseApproval(t))

	want := []string{demomodel.DemoReplyPrefix + "Order 1001: 2 notebooks, shipped."}
	if !slices.Equal(replies, want) {
		t.Fatalf("replies = %q, want %q", replies, want)
	}
	assertExecutedSteps(t, sessionID, "RunModel", "CallTool", "ResumeTurn", "RunModel")
}

func TestEinoQuickStartCancelRunsOnceAfterApproval(t *testing.T) {
	app, orderDesk := startQuickStartApp(t)
	sessionID := startSession(t, app)
	var approvedArguments string

	replies := runTurn(t, app, sessionID, "Cancel order 1002",
		func(_ context.Context, interrupt dexagents.AgentInterrupt) (bool, error) {
			if calls := orderDesk.CancelOrderCalls(); len(calls) != 0 {
				t.Errorf("cancel_order ran before approval: %q", calls)
			}
			approvedArguments = interrupt.Arguments
			return interrupt.ToolName == "cancel_order", nil
		})

	if want := `{"order_id":"1002","reason":"requested by the customer"}`; approvedArguments != want {
		t.Fatalf("approved arguments = %s, want %s", approvedArguments, want)
	}
	if calls := orderDesk.CancelOrderCalls(); !slices.Equal(calls, []string{"1002"}) {
		t.Fatalf("cancel_order calls = %q, want one call for 1002", calls)
	}
	want := []string{demomodel.DemoReplyPrefix + "Order 1002 is cancelled: requested by the customer."}
	if !slices.Equal(replies, want) {
		t.Fatalf("replies = %q, want %q", replies, want)
	}
	assertExecutedSteps(t, sessionID, "RunModel", "AwaitToolApproval", "CallSingleAttemptTool", "ResumeTurn", "RunModel")
}

func TestEinoQuickStartRejectedCancelNeverRuns(t *testing.T) {
	app, orderDesk := startQuickStartApp(t)
	sessionID := startSession(t, app)

	replies := runTurn(t, app, sessionID, "Cancel order 1003",
		func(context.Context, dexagents.AgentInterrupt) (bool, error) { return false, nil })

	if calls := orderDesk.CancelOrderCalls(); len(calls) != 0 {
		t.Fatalf("a rejected cancel_order ran: %q", calls)
	}
	if len(replies) != 1 || !strings.Contains(replies[0], "rejected") {
		t.Fatalf("replies = %q, want the rejection", replies)
	}
}

func TestEinoQuickStartApprovalSurvivesAWorkerRestart(t *testing.T) {
	address := flowServiceAddress(t)
	firstDesk := orderdesk.NewOrderDesk(0)
	firstApp := startQuickStartAppOn(t, address, firstDesk)
	sessionID := startSession(t, firstApp)
	message := "Cancel order 1002"
	ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	defer cancel()
	errStopped := errors.New("stopped at the approval prompt")
	_, err := quickstart.RunTurn(ctx, firstApp.Sessions, sessionID, &message,
		func(context.Context, dexagents.AgentInterrupt) (bool, error) { return false, errStopped })
	if !errors.Is(err, errStopped) {
		t.Fatalf("RunTurn = %v, want it to stop at the approval", err)
	}
	stopApp(t, firstApp)

	secondDesk := orderdesk.NewOrderDesk(0)
	secondApp := startQuickStartAppOn(t, address, secondDesk)
	replies, err := quickstart.RunTurn(ctx, secondApp.Sessions, sessionID, nil,
		func(context.Context, dexagents.AgentInterrupt) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}

	if calls := firstDesk.CancelOrderCalls(); len(calls) != 0 {
		t.Fatalf("the stopped Worker ran cancel_order: %q", calls)
	}
	if calls := secondDesk.CancelOrderCalls(); !slices.Equal(calls, []string{"1002"}) {
		t.Fatalf("cancel_order calls after the restart = %q, want one call for 1002", calls)
	}
	if len(replies) != 1 || !strings.Contains(replies[0], "is cancelled") {
		t.Fatalf("replies = %q, want the cancellation", replies)
	}
}

func TestAskAgentReturnsTheSessionAfterOneTurn(t *testing.T) {
	address := flowServiceAddress(t)
	sessionFlow, err := quickstart.NewDurableEinoAgent(orderdesk.NewOrderDesk(0), demomodel.NewOrderSupportDemoModel())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	defer cancel()
	sessionID := "eino-ask-agent-" + randomHex(t)

	snapshot, err := quickstart.AskAgent(ctx, sessionFlow, sessionID, "Look up order 1003", address, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	last := snapshot.Messages[len(snapshot.Messages)-1]
	if last.Role != dexagents.MessageRoleAssistant || last.Text == nil || !strings.Contains(*last.Text, "3 pens") {
		t.Fatalf("last message = %+v, want the answer about order 1003", last)
	}
}

func TestPlainEinoAgentRunsWithoutDex(t *testing.T) {
	ctx := context.Background()
	agent, err := quickstart.NewPlainEinoAgent(ctx, orderdesk.NewOrderDesk(0), demomodel.NewOrderSupportDemoModel())
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	events := runner.Query(ctx, "Look up order 1001")
	var lastText string
	for {
		event, isOpen := events.Next()
		if !isOpen {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			message, err := event.Output.MessageOutput.GetMessage()
			if err != nil {
				t.Fatal(err)
			}
			lastText = message.Content
		}
	}
	if want := demomodel.DemoReplyPrefix + "Order 1001: 2 notebooks, shipped."; lastText != want {
		t.Fatalf("last message = %q, want %q", lastText, want)
	}
}

func startQuickStartApp(t *testing.T) (*quickstart.QuickStartApp, *orderdesk.OrderDesk) {
	t.Helper()
	orderDesk := orderdesk.NewOrderDesk(0)
	app := startQuickStartAppOn(t, flowServiceAddress(t), orderDesk)
	t.Cleanup(func() { stopApp(t, app) })
	return app, orderDesk
}

func startQuickStartAppOn(t *testing.T, address string, orderDesk *orderdesk.OrderDesk) *quickstart.QuickStartApp {
	t.Helper()
	sessionFlow, err := quickstart.NewDurableEinoAgent(orderDesk, demomodel.NewOrderSupportDemoModel())
	if err != nil {
		t.Fatal(err)
	}
	app, err := quickstart.StartQuickStartApp(context.Background(), sessionFlow, address, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return app
}

var stoppedApps = map[*quickstart.QuickStartApp]bool{}

func stopApp(t *testing.T, app *quickstart.QuickStartApp) {
	t.Helper()
	if stoppedApps[app] {
		return
	}
	stoppedApps[app] = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Errorf("stop the quick start app: %v", err)
	}
}

func startSession(t *testing.T, app *quickstart.QuickStartApp) string {
	t.Helper()
	sessionID := "eino-quick-start-test-" + randomHex(t)
	if _, err := app.Sessions.StartSession(context.Background(), sessionID, nil, dex.StartFlowOptions{}); err != nil {
		t.Fatal(err)
	}
	return sessionID
}

func runTurn(
	t *testing.T, app *quickstart.QuickStartApp, sessionID string, message string, decide quickstart.ApprovalDecider,
) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	defer cancel()
	replies, err := quickstart.RunTurn(ctx, app.Sessions, sessionID, &message, decide)
	if err != nil {
		t.Fatal(err)
	}
	return replies
}

func refuseApproval(t *testing.T) quickstart.ApprovalDecider {
	return func(_ context.Context, interrupt dexagents.AgentInterrupt) (bool, error) {
		t.Errorf("unexpected approval for %s", interrupt.ToolName)
		return false, nil
	}
}

func assertExecutedSteps(t *testing.T, sessionID string, want ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	history, err := agentstest.ReadStepHistory(ctx, dexcliPath(t), flowServiceAddress(t), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	executed := agentstest.ExecutedStepTypes(history)
	// The first Steps start the session and take the message; the turn's Steps follow.
	turnStart := slices.Index(executed, "RunModel")
	if turnStart < 0 || !slices.Equal(executed[turnStart:], want) {
		t.Fatalf("executed Steps = %q, want the turn %q", executed, want)
	}
}

func flowServiceAddress(t *testing.T) string {
	t.Helper()
	address := os.Getenv("DEX_FLOW_SERVICE_ADDRESS")
	if address == "" {
		if os.Getenv("DEX_EXAMPLES_ALLOW_SKIP") == "1" {
			t.Skip("DEX_FLOW_SERVICE_ADDRESS is not set")
		}
		t.Fatal("set DEX_FLOW_SERVICE_ADDRESS to a running Dex server, or DEX_EXAMPLES_ALLOW_SKIP=1")
	}
	return address
}

func dexcliPath(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"DEXCLI_PATH", "DEXCLI"} {
		if path := os.Getenv(name); path != "" {
			return path
		}
	}
	path, err := exec.LookPath("dexcli")
	if err != nil {
		t.Fatal("set DEXCLI_PATH or DEXCLI to the dexcli executable, or put dexcli on PATH")
	}
	return path
}

func randomHex(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buffer)
}
