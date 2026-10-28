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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aiagent "github.com/superdurable/dex/examples/go/products/ai-agent"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The AI Agent app runs as its own process so a test can SIGKILL it. The engine keeps running.
// The shared in-process Worker does not register AIAgentFlow, so only this app serves agents.

const aiAgentConvergenceTimeout = 90 * time.Second

func TestAIAgentResumesEveryWaitAfterTheAppIsKilled(t *testing.T) {
	app := startAIAgentApp(t)
	flowID := app.startAgent(t)

	app.sendUserMessage(t, flowID, "/ask Which release ships today?")
	asked := app.waitForSnapshot(t, flowID, "the agent's question", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForAnswer
	})
	app.restartAfterKill(t)
	afterRestart := app.getAgentSnapshot(t, flowID)
	require.Equal(t, asked.PendingQuestion, afterRestart.PendingQuestion)
	require.Equal(t, asked.Messages, afterRestart.Messages)
	app.sendUserMessage(t, flowID, "Aurora")
	answered := app.waitForToolResult(t, flowID, aiagent.ToolAskUser)
	require.Equal(t, "The user answered: Aurora", answered.Content)

	app.sendUserMessage(t, flowID, "/publish Aurora ships today")
	waiting := app.waitForSnapshot(t, flowID, "the approval request", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForApproval
	})
	app.restartAfterKill(t)
	require.Equal(t, waiting.PendingApproval, app.getAgentSnapshot(t, flowID).PendingApproval)
	require.True(t, app.decideToolApproval(t, flowID, waiting.PendingApproval.ID, true))
	published := app.waitForToolResult(t, flowID, aiagent.ToolPublishNote)
	require.Equal(t, aiagent.OutcomeDone, published.Outcome)
	notes, err := app.toolbox.ReadPublishedNotes()
	require.NoError(t, err)
	require.Len(t, notes, 1)
	require.Equal(t, waiting.PendingApproval.ID, notes[0].CallID)
	require.False(t, app.decideToolApproval(t, flowID, waiting.PendingApproval.ID, true), "a second decision has no pending call")

	app.sendUserMessage(t, flowID, "/wait 3")
	timer := app.waitForSnapshot(t, flowID, "the durable Timer", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForTimer
	})
	app.kill(t)
	// The behavior under test: the Timer fires while no Worker is running.
	time.Sleep(time.Until(timer.PendingTimer.FiresAt) + time.Second)
	app.start(t)
	waited := app.waitForToolResult(t, flowID, aiagent.ToolWait)
	require.Equal(t, "Waited 3 seconds.", waited.Content)
}

func TestAIAgentRepeatsAReadOnlyToolThatTheKillCutOff(t *testing.T) {
	var pageRequests atomic.Int32
	pageRequested := make(chan struct{}, 1)
	page := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		pageRequests.Add(1)
		select {
		case pageRequested <- struct{}{}:
		default:
		}
		if _, err := fmt.Fprint(writer, "<html><head><title>Release notes</title></head><body><p>Aurora ships today.</p></body></html>"); err != nil {
			t.Errorf("write the page: %v", err)
		}
	}))
	t.Cleanup(page.Close)
	app := startAIAgentApp(t, "DEX_AI_AGENT_STAGE_PAUSE=1s")
	flowID := app.startAgent(t)

	app.sendUserMessage(t, flowID, "/fetch "+page.URL)
	select {
	case <-pageRequested:
	case <-time.After(aiAgentConvergenceTimeout):
		t.Fatal("fetch_page never downloaded the page")
	}
	app.restartAfterKill(t)

	result := app.waitForToolResult(t, flowID, aiagent.ToolFetchPage)
	require.Equal(t, aiagent.OutcomeDone, result.Outcome)
	require.Contains(t, result.Content, "Title: Release notes")
	require.Contains(t, result.Content, "Dex ran this read-only call again")
	require.Equal(t, int32(2), pageRequests.Load())
}

func TestAIAgentReportsAWriteThatTheKillCutOffAsOutcomeUnknown(t *testing.T) {
	app := startAIAgentApp(t, "DEX_AI_AGENT_STAGE_PAUSE=3s")
	flowID := app.startAgent(t)

	app.sendUserMessage(t, flowID, "/publish Aurora ships today")
	waiting := app.waitForSnapshot(t, flowID, "the approval request", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForApproval
	})
	require.True(t, app.decideToolApproval(t, flowID, waiting.PendingApproval.ID, true))
	app.waitForSnapshot(t, flowID, "the publish_note call", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusExecutingTool
	})
	app.restartAfterKill(t)

	result := app.waitForToolResult(t, flowID, aiagent.ToolPublishNote)
	require.Equal(t, aiagent.OutcomeOutcomeUnknown, result.Outcome)
	require.Contains(t, result.Content, "did not repeat it because it changes data")
	notes, err := app.toolbox.ReadPublishedNotes()
	require.NoError(t, err)
	require.Empty(t, notes, "Dex must not publish the note again after the kill")
}

func TestAIAgentFlagsAModelProviderAPIFailure(t *testing.T) {
	var providerRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		if _, err := fmt.Fprint(writer, `{"error":{"message":"The model is overloaded."}}`); err != nil {
			t.Errorf("write the provider error: %v", err)
		}
	}))
	t.Cleanup(provider.Close)
	app := startAIAgentApp(t, "OPENAI_API_KEY=test-key", "OPENAI_BASE_URL="+provider.URL+"/v1")
	flowID := app.startAgent(t)

	app.sendUserMessage(t, flowID, "hello")
	failed := app.waitForSnapshot(t, flowID, "the model failure", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForUser && snapshot.LastModelFailure != nil
	})
	require.True(t, failed.LastModelFailure.IsProviderAPIError)
	require.Equal(t, aiagent.ModelFailureProviderAPIError, failed.LastModelFailure.Kind)
	require.Contains(t, failed.LastModelFailure.Detail, "HTTP 503: The model is overloaded.")
	require.Equal(t, int32(3), providerRequests.Load(), "CallModel retries twice before its failure route")
}

func TestAIAgentPostsToSlackThroughTheDexConnector(t *testing.T) {
	var postsMutex sync.Mutex
	var posts []map[string]string
	fakeSlack := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat.postMessage" || request.Header.Get("Authorization") != "Bearer xoxb-test-token" {
			t.Errorf("unexpected Slack request %s", request.URL.Path)
		}
		var post map[string]string
		if err := json.NewDecoder(request.Body).Decode(&post); err != nil {
			t.Errorf("decode the Slack request: %v", err)
		}
		postsMutex.Lock()
		posts = append(posts, post)
		postsMutex.Unlock()
		if strings.Contains(post["text"], "unconfirmed") {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if _, err := fmt.Fprintf(writer, `{"ok":true,"channel":%q,"ts":"1761700000.000100","message":{"text":%q,"ts":"1761700000.000100"}}`,
			post["channel"], post["text"]); err != nil {
			t.Errorf("write the Slack response: %v", err)
		}
	}))
	t.Cleanup(fakeSlack.Close)
	app := startAIAgentApp(t,
		"SLACK_BOT_TOKEN=xoxb-test-token", "DEX_AI_AGENT_SLACK_CHANNEL=C0DEMO", "DEX_AI_AGENT_SLACK_ENDPOINT="+fakeSlack.URL)
	flowID := app.startAgent(t)

	app.sendUserMessage(t, flowID, "/slack Aurora ships today")
	waiting := app.waitForSnapshot(t, flowID, "the Slack approval", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForApproval
	})
	app.restartAfterKill(t)
	require.True(t, app.decideToolApproval(t, flowID, waiting.PendingApproval.ID, true))
	posted := app.waitForToolResult(t, flowID, aiagent.ToolPostToSlack)
	require.Equal(t, aiagent.OutcomeDone, posted.Outcome, posted.Content)
	require.Equal(t, "Posted to Slack channel C0DEMO (message 1761700000.000100).", posted.Content)

	app.sendUserMessage(t, flowID, "/slack This post stays unconfirmed")
	waiting = app.waitForSnapshot(t, flowID, "the second Slack approval", func(snapshot aiagent.AgentSnapshot) bool {
		return snapshot.Status == aiagent.StatusWaitingForApproval
	})
	require.True(t, app.decideToolApproval(t, flowID, waiting.PendingApproval.ID, true))
	unconfirmed := app.waitForToolResult(t, flowID, aiagent.ToolPostToSlack)
	require.Equal(t, aiagent.OutcomeOutcomeUnknown, unconfirmed.Outcome)
	require.Contains(t, unconfirmed.Content, "Slack did not confirm the message")

	postsMutex.Lock()
	defer postsMutex.Unlock()
	require.Len(t, posts, 2, "the connector sends each approved post once")
	require.Equal(t, "C0DEMO", posts[0]["channel"])
	require.Equal(t, "Aurora ships today", posts[0]["text"])
	require.NotEmpty(t, posts[0]["client_msg_id"])
}

type aiAgentApp struct {
	binary      string
	environment []string
	baseURL     string
	logPath     string
	toolbox     *aiagent.Toolbox
	process     *exec.Cmd
	flowIDs     []string
}

func startAIAgentApp(t *testing.T, environment ...string) *aiAgentApp {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "dex-ai-agent")
	build := exec.Command("go", "build", "-o", binary, "github.com/superdurable/dex/examples/go/cmd/ai-agent")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	notesFile := filepath.Join(directory, "notes.jsonl")
	app := &aiAgentApp{
		binary: binary,
		environment: append([]string{
			"DEX_FLOW_SERVICE_ADDRESS=" + flowServiceAddress(),
			"DEX_AI_AGENT_HTTP_ADDRESS=" + address,
			"DEX_AI_AGENT_NOTES_FILE=" + notesFile,
			"DEX_BLOB_CACHE_DIR=" + filepath.Join(directory, "blobs"),
		}, environment...),
		baseURL: "http://" + address + "/ai-agent",
		logPath: filepath.Join(directory, "app.log"),
		toolbox: &aiagent.Toolbox{NotesFile: notesFile},
	}
	t.Cleanup(func() { app.cleanUp(t) })
	app.start(t)
	return app
}

func (app *aiAgentApp) start(t *testing.T) {
	t.Helper()
	logFile, err := os.OpenFile(app.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer func() { require.NoError(t, logFile.Close()) }()
	logOffset, err := logFile.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	app.process = exec.Command(app.binary)
	app.process.Env = append(os.Environ(), app.environment...)
	app.process.Stdout, app.process.Stderr = logFile, logFile
	require.NoError(t, app.process.Start())
	require.Eventually(t, func() bool {
		return strings.Contains(app.readLogFrom(t, logOffset), "Dex AI Agent ready at")
	}, time.Minute, 50*time.Millisecond, "the AI Agent app did not start")
}

// kill sends SIGKILL, so the app gets no chance to finish or report its work.
func (app *aiAgentApp) kill(t *testing.T) {
	t.Helper()
	if app.process == nil {
		return
	}
	require.NoError(t, app.process.Process.Kill())
	err := app.process.Wait()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError)
	app.process = nil
}

func (app *aiAgentApp) restartAfterKill(t *testing.T) {
	t.Helper()
	app.kill(t)
	app.start(t)
}

func (app *aiAgentApp) cleanUp(t *testing.T) {
	app.kill(t)
	for _, flowID := range app.flowIDs {
		err := integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{Reason: "test finished"})
		var notActive *dex.FlowNotActiveOrNotFoundError
		if err != nil && !errors.As(err, &notActive) {
			t.Errorf("stop %s: %v", flowID, err)
		}
	}
	if t.Failed() {
		t.Logf("AI Agent app log:\n%s", app.readLogFrom(t, 0))
	}
}

func (app *aiAgentApp) startAgent(t *testing.T) string {
	t.Helper()
	var started struct {
		FlowID string `json:"flowId"`
	}
	app.postJSON(t, "/agents", map[string]string{"flowId": newFlowID(t, "ai-agent")}, &started)
	app.flowIDs = append(app.flowIDs, started.FlowID)
	return started.FlowID
}

func (app *aiAgentApp) sendUserMessage(t *testing.T, flowID, text string) {
	t.Helper()
	var response struct {
		IsQueued bool `json:"isQueued"`
	}
	app.postJSON(t, "/agents/"+flowID+"/messages", map[string]string{"text": text}, &response)
	require.True(t, response.IsQueued)
}

func (app *aiAgentApp) decideToolApproval(t *testing.T, flowID, callID string, isApproved bool) bool {
	t.Helper()
	var response struct {
		IsAccepted bool `json:"isAccepted"`
	}
	app.postJSON(t, "/agents/"+flowID+"/approvals", aiagent.ToolApprovalDecision{CallID: callID, IsApproved: isApproved}, &response)
	return response.IsAccepted
}

func (app *aiAgentApp) getAgentSnapshot(t *testing.T, flowID string) aiagent.AgentSnapshot {
	t.Helper()
	snapshot, err := app.fetchAgentSnapshot(flowID)
	require.NoError(t, err)
	return snapshot
}

func (app *aiAgentApp) waitForSnapshot(
	t *testing.T,
	flowID string,
	description string,
	isReady func(aiagent.AgentSnapshot) bool,
) aiagent.AgentSnapshot {
	t.Helper()
	var snapshot aiagent.AgentSnapshot
	var lastErr error
	deadline := time.Now().Add(aiAgentConvergenceTimeout)
	for time.Now().Before(deadline) {
		snapshot, lastErr = app.fetchAgentSnapshot(flowID)
		if lastErr == nil && isReady(snapshot) {
			return snapshot
		}
		time.Sleep(100 * time.Millisecond)
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	require.NoError(t, err)
	t.Fatalf("timed out waiting for %s in %s (last error %v):\n%s", description, flowID, lastErr, encoded)
	return snapshot
}

// waitForToolResult waits until the agent is back with the user and returns the tool's last result.
func (app *aiAgentApp) waitForToolResult(t *testing.T, flowID, toolName string) aiagent.Message {
	t.Helper()
	var result aiagent.Message
	app.waitForSnapshot(t, flowID, "a "+toolName+" result", func(snapshot aiagent.AgentSnapshot) bool {
		if snapshot.Status != aiagent.StatusWaitingForUser {
			return false
		}
		for _, message := range snapshot.Messages {
			if message.Role == aiagent.RoleTool && message.ToolName == toolName {
				result = message
			}
		}
		return result.ToolCallID != ""
	})
	return result
}

func (app *aiAgentApp) fetchAgentSnapshot(flowID string) (aiagent.AgentSnapshot, error) {
	var snapshot aiagent.AgentSnapshot
	response, err := http.Get(app.baseURL + "/agents/" + flowID)
	if err != nil {
		return snapshot, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return snapshot, fmt.Errorf("snapshot returned HTTP %d", response.StatusCode)
	}
	return snapshot, json.NewDecoder(response.Body).Decode(&snapshot)
}

func (app *aiAgentApp) postJSON(t *testing.T, path string, body any, output any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, app.baseURL+path, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, json.NewDecoder(response.Body).Decode(output))
}

func (app *aiAgentApp) readLogFrom(t *testing.T, offset int64) string {
	t.Helper()
	content, err := os.ReadFile(app.logPath)
	require.NoError(t, err)
	if offset > int64(len(content)) {
		return ""
	}
	return string(content[offset:])
}
