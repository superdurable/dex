// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package demosession plays a scripted fake agent session, so the chat screen
// runs without Dex.
package demosession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// DemoSessionConfig configures NewDemoSession.
type DemoSessionConfig struct {
	// Pace scales every scripted delay. Default 0 plays without delays; the demo
	// command passes 1.
	Pace float64
	// Now returns the current time for tool start times. Default: time.Now.
	Now func() time.Time
}

// DemoSession is a scripted ChatSessionSource: a tool turn with approvals,
// then a failed turn, then echoes.
type DemoSession struct {
	pace    float64
	now     func() time.Time
	updates chan sessionfeed.SessionUpdate
	actions chan sessionfeed.UserAction

	startOnce   sync.Once
	mutex       sync.Mutex
	queued      []sessionfeed.QueuedMessage
	messageID   int
	scriptError error
}

// errDemoTurnCancelled ends a scripted turn after the user cancelled it.
var errDemoTurnCancelled = errors.New("demo turn cancelled")

const (
	demoCommand   = "openai-sdk"
	demoModel     = "gpt-5.2"
	demoSessionID = "openai-sdk-20261111-142233-a1b2c3"
)

// NewDemoSession returns a demo session. The first NextSessionUpdate context
// runs the script.
func NewDemoSession(config DemoSessionConfig) *DemoSession {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &DemoSession{
		pace:    config.Pace,
		now:     now,
		updates: make(chan sessionfeed.SessionUpdate),
		actions: make(chan sessionfeed.UserAction, 16),
	}
}

// NextSessionUpdate returns the next scripted update.
func (demo *DemoSession) NextSessionUpdate(ctx context.Context) (sessionfeed.SessionUpdate, error) {
	demo.startOnce.Do(func() {
		go demo.playScript(ctx)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case update, isOpen := <-demo.updates:
		if !isOpen {
			return nil, sessionfeed.ErrSessionFeedClosed
		}
		return update, nil
	}
}

// SubmitUserAction hands an action to the script.
func (demo *DemoSession) SubmitUserAction(ctx context.Context, action sessionfeed.UserAction) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case demo.actions <- action:
		return nil
	}
}

// DemoSlashCommands returns the slash commands the demo session runs.
func DemoSlashCommands() []sessionfeed.SlashCommand {
	return []sessionfeed.SlashCommand{
		{Name: "model", ArgumentHint: "ID", Description: "change the model"},
		{Name: "image", ArgumentHint: "PATH", Description: "attach an image to the next message"},
		{Name: "sessions", Description: "switch session"},
		{Name: "new", Description: "start a session"},
		{Name: "web", Description: "open in Dex Web"},
	}
}

// ScriptError returns the error that ended the script, or nil while it
// plays. The script ends only when its context ends.
func (demo *DemoSession) ScriptError() error {
	demo.mutex.Lock()
	defer demo.mutex.Unlock()
	return demo.scriptError
}

func (demo *DemoSession) playScript(ctx context.Context) {
	defer close(demo.updates)
	script := demoScript{demo: demo, ctx: ctx}
	err := script.play()
	demo.mutex.Lock()
	defer demo.mutex.Unlock()
	demo.scriptError = err
}

// demoScript plays the scripted conversation on one goroutine.
type demoScript struct {
	demo              *DemoSession
	ctx               context.Context
	turnNumber        int
	openInterruptIDs  []string
	awaitingToolCalls []sessionfeed.ToolCallChanged
	// deferredActions holds approvals and answers that arrived while the
	// script waited for something else.
	deferredActions []sessionfeed.UserAction
}

func (script *demoScript) play() error {
	if err := script.send(sessionfeed.SessionOpened{
		Command:            demoCommand,
		Model:              demoModel,
		SessionID:          demoSessionID,
		IsNewSession:       true,
		AcceptsTurnControl: true,
	}); err != nil {
		return err
	}
	if err := script.pause(400 * time.Millisecond); err != nil {
		return err
	}
	if err := script.send(sessionfeed.SessionLoaded{}); err != nil {
		return err
	}
	model := demoModel
	for {
		action, err := script.nextAction()
		if err != nil {
			return err
		}
		switch typed := action.(type) {
		case sessionfeed.SendUserMessage:
			if err := script.playTurns(typed.Text, false); err != nil {
				return err
			}
		case sessionfeed.RunSlashCommand:
			if typed.Name == "model" && typed.Argument != "" {
				model = typed.Argument
				if err := script.send(sessionfeed.SessionOpened{
					Command: demoCommand, Model: model, SessionID: demoSessionID,
					TurnCount: script.turnNumber, IsNewSession: script.turnNumber == 0, AcceptsTurnControl: true,
				}); err != nil {
					return err
				}
				if err := script.notice(sessionfeed.NoticeSeverityInfo, "model set to "+model); err != nil {
					return err
				}
				continue
			}
			if err := script.notice(sessionfeed.NoticeSeverityWarning, "/"+typed.Name+" is not part of this demo."); err != nil {
				return err
			}
		}
	}
}

// playTurns plays a turn for text, then one turn per queued message.
func (script *demoScript) playTurns(text string, isFromQueue bool) error {
	for {
		if isFromQueue {
			if err := script.send(sessionfeed.UserMessageRecorded{Text: text}); err != nil {
				return err
			}
		}
		script.turnNumber++
		turnID := fmt.Sprintf("turn-%d", script.turnNumber)
		if err := script.send(sessionfeed.TurnStarted{TurnID: turnID, TurnNumber: script.turnNumber, StartedAt: script.demo.now()}); err != nil {
			return err
		}
		var err error
		switch script.turnNumber {
		case 1:
			err = script.playToolTurn(turnID)
		case 2:
			err = script.playFailingTurn(turnID)
		default:
			err = script.playShortTurn(turnID, text)
		}
		switch {
		case errors.Is(err, errDemoTurnCancelled):
			script.deferredActions = nil
			if err := script.withdrawPendingWork(); err != nil {
				return err
			}
			if err := script.send(sessionfeed.TurnEnded{TurnID: turnID, Outcome: sessionfeed.TurnOutcomeCancelled}); err != nil {
				return err
			}
		case err != nil:
			return err
		}
		next, hasNext := script.demo.popQueuedMessage()
		if !hasNext {
			return nil
		}
		if err := script.sendQueued(); err != nil {
			return err
		}
		text, isFromQueue = next.Text, true
	}
}

func (script *demoScript) playToolTurn(turnID string) error {
	steps := []func() error{
		func() error {
			return script.reason(turnID, "The user wants flaky test issues. Search the tracker, then read the test.")
		},
		func() error {
			return script.streamReply(turnID, "step-1", 1, "I'll search the issue tracker first.\n\n")
		},
		func() error {
			return script.send(sessionfeed.AssistantMessageRecorded{TurnID: turnID, StepExecutionID: "step-1", Text: "I'll search the issue tracker first."})
		},
		func() error { return script.activity(turnID, sessionfeed.TurnActivityRunningTools) },
		func() error {
			return script.runTool("call-1", "search_files", `"flaky" in tests/`, "12 matches", 400*time.Millisecond, nil)
		},
		func() error {
			return script.runTool("call-2", "read_file", "tests/integ/retry_test.go", "210 lines", 100*time.Millisecond, nil)
		},
		func() error {
			return script.runTool("call-3", "run_shell_command", "go test ./integ/... -run Retry", "exit 0", 3*time.Second, []string{
				"=== RUN   TestRetryBackoff",
				"--- PASS: TestRetryBackoff (0.31s)",
				"=== RUN   TestRetryJitter",
				"--- PASS: TestRetryJitter (0.12s)",
				"PASS",
				"ok  	example.com/integ	0.84s",
			})
		},
		func() error { return script.usage(1240) },
		func() error { return script.askBranch(turnID) },
		func() error { return script.requestApprovals(turnID) },
		func() error { return script.activity(turnID, sessionfeed.TurnActivityRunningModel) },
		func() error {
			return script.streamReply(turnID, "step-2", 1, demoFinalReply)
		},
		func() error {
			return script.send(sessionfeed.AssistantMessageRecorded{TurnID: turnID, StepExecutionID: "step-2", Text: demoFinalReply})
		},
		func() error { return script.usage(3870) },
		func() error {
			return script.send(sessionfeed.TurnEnded{TurnID: turnID, Outcome: sessionfeed.TurnOutcomeCompleted})
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

const demoFinalReply = "Found 3 open issues. Draft summary:\n\n" +
	"- **#812** TestRetryBackoff times out on arm64\n" +
	"- **#844** TestRetryJitter fails when `GOMAXPROCS=1`\n" +
	"- **#851** nightly job retries the whole suite\n\n" +
	"The backoff fix:\n\n" +
	"```go\nbackoff := 2 * time.Second\n```\n"

func (script *demoScript) playFailingTurn(turnID string) error {
	if err := script.streamReply(turnID, "step-1", 1, "Checking the nightly job configuration. The job runs"); err != nil {
		return err
	}
	if err := script.pause(600 * time.Millisecond); err != nil {
		return err
	}
	if err := script.streamReply(turnID, "step-1", 2, "Checking the nightly job configuration again."); err != nil {
		return err
	}
	return script.send(sessionfeed.TurnEnded{
		TurnID:      turnID,
		Outcome:     sessionfeed.TurnOutcomeFailed,
		FailureText: "The turn failed: model call returned 401 (invalid API key). Set OPENAI_API_KEY and send again.",
	})
}

func (script *demoScript) playShortTurn(turnID string, text string) error {
	reply := fmt.Sprintf("You said: %s\n\nThis demo has no more scripted turns.", text)
	if err := script.streamReply(turnID, "step-1", 1, reply); err != nil {
		return err
	}
	if err := script.send(sessionfeed.AssistantMessageRecorded{TurnID: turnID, StepExecutionID: "step-1", Text: reply}); err != nil {
		return err
	}
	return script.send(sessionfeed.TurnEnded{TurnID: turnID, Outcome: sessionfeed.TurnOutcomeCompleted})
}

func (script *demoScript) reason(turnID string, text string) error {
	for _, word := range strings.SplitAfter(text, " ") {
		if err := script.send(sessionfeed.ReasoningDelta{TurnID: turnID, Text: word}); err != nil {
			return err
		}
		if err := script.pauseInTurn(60*time.Millisecond, nil); err != nil {
			return err
		}
	}
	return nil
}

func (script *demoScript) streamReply(turnID string, stepExecutionID string, attempt int, text string) error {
	for _, chunk := range splitIntoChunks(text, 6) {
		if err := script.send(sessionfeed.AssistantTextDelta{TurnID: turnID, StepExecutionID: stepExecutionID, Attempt: attempt, Text: chunk}); err != nil {
			return err
		}
		if err := script.pauseInTurn(40*time.Millisecond, nil); err != nil {
			return err
		}
	}
	return nil
}

func (script *demoScript) runTool(callID string, toolName string, keyArgument string, summary string, duration time.Duration, output []string) error {
	startedAt := script.demo.now()
	call := sessionfeed.ToolCallChanged{
		ToolCallID: callID, ToolName: toolName, KeyArgument: keyArgument,
		Status: sessionfeed.ToolCallRunning, StartedAt: startedAt,
	}
	if err := script.send(call); err != nil {
		return err
	}
	step := duration / time.Duration(len(output)+1)
	for index := range output {
		if err := script.pauseInTurn(step, nil); err != nil {
			return script.endToolOnError(call, err)
		}
		call.OutputTail = output[max(index-2, 0) : index+1]
		call.HiddenOutputLineCount = max(index-2, 0)
		if err := script.send(call); err != nil {
			return err
		}
	}
	if err := script.pauseInTurn(step, nil); err != nil {
		return script.endToolOnError(call, err)
	}
	call.Status = sessionfeed.ToolCallSucceeded
	call.Summary = summary
	call.Duration = duration
	call.OutputTail = nil
	call.HiddenOutputLineCount = 0
	return script.send(call)
}

// endToolOnError marks a running call cancelled when the turn was
// cancelled, and returns err.
func (script *demoScript) endToolOnError(call sessionfeed.ToolCallChanged, err error) error {
	if !errors.Is(err, errDemoTurnCancelled) {
		return err
	}
	call.Status = sessionfeed.ToolCallCancelled
	call.OutputTail = nil
	call.HiddenOutputLineCount = 0
	if sendErr := script.send(call); sendErr != nil {
		return sendErr
	}
	return err
}

// withdrawPendingWork ends the interrupts and waiting tool calls of a
// cancelled turn.
func (script *demoScript) withdrawPendingWork() error {
	for _, interruptID := range script.openInterruptIDs {
		if err := script.send(sessionfeed.InterruptResolved{InterruptID: interruptID, Resolution: sessionfeed.InterruptWithdrawn}); err != nil {
			return err
		}
	}
	for _, call := range script.awaitingToolCalls {
		call.Status = sessionfeed.ToolCallCancelled
		if err := script.send(call); err != nil {
			return err
		}
	}
	script.openInterruptIDs, script.awaitingToolCalls = nil, nil
	return nil
}

func (script *demoScript) forgetInterrupt(interruptID string) {
	for index, openID := range script.openInterruptIDs {
		if openID == interruptID {
			script.openInterruptIDs = append(script.openInterruptIDs[:index], script.openInterruptIDs[index+1:]...)
			return
		}
	}
}

func (script *demoScript) forgetAwaitingToolCall(toolCallID string) {
	for index, call := range script.awaitingToolCalls {
		if call.ToolCallID == toolCallID {
			script.awaitingToolCalls = append(script.awaitingToolCalls[:index], script.awaitingToolCalls[index+1:]...)
			return
		}
	}
}

func (script *demoScript) askBranch(turnID string) error {
	if err := script.activity(turnID, sessionfeed.TurnActivityAwaitingUser); err != nil {
		return err
	}
	if err := script.send(sessionfeed.QuestionAsked{
		InterruptID:    "question-1",
		Question:       "Which branch should I compare against?",
		Choices:        []string{"main", "release-0.6"},
		AllowsFreeText: true,
	}); err != nil {
		return err
	}
	script.openInterruptIDs = append(script.openInterruptIDs, "question-1")
	action, err := script.waitForAction(func(action sessionfeed.UserAction) bool {
		switch action.(type) {
		case sessionfeed.AnswerQuestion, sessionfeed.DeclineQuestion:
			return true
		}
		return false
	})
	if err != nil {
		return err
	}
	script.forgetInterrupt("question-1")
	resolution := sessionfeed.InterruptAnswered
	if _, isDeclined := action.(sessionfeed.DeclineQuestion); isDeclined {
		resolution = sessionfeed.InterruptDeclined
	}
	return script.send(sessionfeed.InterruptResolved{InterruptID: "question-1", Resolution: resolution})
}

func (script *demoScript) requestApprovals(turnID string) error {
	slackCall := sessionfeed.ToolCallChanged{
		ToolCallID: "call-4", ToolName: "slack_post_channel_message", KeyArgument: "#eng",
		Status: sessionfeed.ToolCallAwaitingApproval, StartedAt: script.demo.now(),
	}
	editCall := sessionfeed.ToolCallChanged{
		ToolCallID: "call-5", ToolName: "edit_file", KeyArgument: "src/retry.go",
		Status: sessionfeed.ToolCallAwaitingApproval, StartedAt: script.demo.now(),
	}
	approvals := []sessionfeed.SessionUpdate{
		slackCall, editCall,
		sessionfeed.ApprovalRequested{
			InterruptID: "approval-1", ToolCallID: "call-4", ToolName: "slack_post_channel_message",
			SourceLabel: "connector", ScopeLabel: "slack/support-slack", EffectLabel: "write",
			Arguments: []sessionfeed.ApprovalArgument{
				{Name: "channel", Value: "#eng"},
				{Name: "text", Value: "Found 3 open issues about flaky tests: #812 TestRetryBackoff times out on arm64; #844 TestRetryJitter fails when GOMAXPROCS=1; #851 the nightly job retries the whole suite instead of the failed package. Proposed fix: double the base backoff and retry only failed packages."},
			},
			EffectSummary: "Posts to #eng with connection support-slack.",
			Deadline:      script.demo.now().Add(9*time.Minute + 30*time.Second),
		},
		sessionfeed.ApprovalRequested{
			InterruptID: "approval-2", ToolCallID: "call-5", ToolName: "edit_file",
			SourceLabel: "local file", ScopeLabel: "workspace", EffectLabel: "write",
			Arguments: []sessionfeed.ApprovalArgument{{Name: "path", Value: "src/retry.go"}},
			Preview: &sessionfeed.FileChangePreview{
				Path: "src/retry.go", AddedLineCount: 1, RemovedLineCount: 1,
				Lines: []sessionfeed.DiffLine{
					{Kind: sessionfeed.DiffLineContext, LineNumber: 11, Text: "func nextBackoff(attempt int) time.Duration {"},
					{Kind: sessionfeed.DiffLineRemoved, LineNumber: 12, Text: "\tbackoff := time.Second"},
					{Kind: sessionfeed.DiffLineAdded, LineNumber: 12, Text: "\tbackoff := 2 * time.Second"},
				},
			},
			EffectSummary: "Changes src/retry.go in the workspace.",
		},
	}
	if err := script.activity(turnID, sessionfeed.TurnActivityAwaitingUser); err != nil {
		return err
	}
	for _, update := range approvals {
		if err := script.send(update); err != nil {
			return err
		}
	}
	script.openInterruptIDs = append(script.openInterruptIDs, "approval-1", "approval-2")
	script.awaitingToolCalls = append(script.awaitingToolCalls, slackCall, editCall)
	calls := map[string]*sessionfeed.ToolCallChanged{"approval-1": &slackCall, "approval-2": &editCall}
	summaries := map[string]string{"approval-1": "posted", "approval-2": "+1 -1"}
	for len(calls) > 0 {
		action, err := script.waitForAction(func(action sessionfeed.UserAction) bool {
			_, isApproval := action.(sessionfeed.ResolveApproval)
			return isApproval
		})
		if err != nil {
			return err
		}
		resolve := action.(sessionfeed.ResolveApproval)
		call, exists := calls[resolve.InterruptID]
		if !exists {
			continue
		}
		delete(calls, resolve.InterruptID)
		script.forgetInterrupt(resolve.InterruptID)
		script.forgetAwaitingToolCall(call.ToolCallID)
		resolution := sessionfeed.InterruptRejected
		if resolve.IsApproved {
			resolution = sessionfeed.InterruptApproved
		}
		if err := script.send(sessionfeed.InterruptResolved{InterruptID: resolve.InterruptID, Resolution: resolution}); err != nil {
			return err
		}
		if !resolve.IsApproved {
			call.Status = sessionfeed.ToolCallRejected
			call.Reason = resolve.Reason
			if err := script.send(*call); err != nil {
				return err
			}
			continue
		}
		call.Status = sessionfeed.ToolCallRunning
		call.StartedAt = script.demo.now()
		if err := script.send(*call); err != nil {
			return err
		}
		if err := script.pauseInTurn(800*time.Millisecond, nil); err != nil {
			return err
		}
		call.Status = sessionfeed.ToolCallSucceeded
		call.Summary = summaries[resolve.InterruptID]
		call.Duration = 800 * time.Millisecond
		if err := script.send(*call); err != nil {
			return err
		}
	}
	return nil
}

func (script *demoScript) activity(turnID string, activity sessionfeed.TurnActivity) error {
	return script.send(sessionfeed.TurnActivityChanged{TurnID: turnID, Activity: activity})
}

func (script *demoScript) usage(tokens int64) error {
	return script.send(sessionfeed.UsageRecorded{TotalTokens: tokens})
}

func (script *demoScript) notice(severity sessionfeed.NoticeSeverity, text string) error {
	return script.send(sessionfeed.NoticeRaised{Severity: severity, Text: text})
}

func (script *demoScript) send(update sessionfeed.SessionUpdate) error {
	select {
	case <-script.ctx.Done():
		return script.ctx.Err()
	case script.demo.updates <- update:
		return nil
	}
}

func (script *demoScript) sendQueued() error {
	return script.send(sessionfeed.QueuedMessagesChanged{Messages: script.demo.queuedMessages()})
}

// nextAction waits for the next user action between turns.
func (script *demoScript) nextAction() (sessionfeed.UserAction, error) {
	select {
	case <-script.ctx.Done():
		return nil, script.ctx.Err()
	case action := <-script.demo.actions:
		return action, nil
	}
}

// waitForAction waits for an action isWanted accepts, handling queue, steer,
// delete, and cancel meanwhile.
func (script *demoScript) waitForAction(isWanted func(sessionfeed.UserAction) bool) (sessionfeed.UserAction, error) {
	for index, deferred := range script.deferredActions {
		if isWanted(deferred) {
			script.deferredActions = append(script.deferredActions[:index], script.deferredActions[index+1:]...)
			return deferred, nil
		}
	}
	for {
		select {
		case <-script.ctx.Done():
			return nil, script.ctx.Err()
		case action := <-script.demo.actions:
			if isWanted(action) {
				return action, nil
			}
			if err := script.handleTurnAction(action); err != nil {
				return nil, err
			}
		}
	}
}

func (script *demoScript) pause(delay time.Duration) error {
	scaled := time.Duration(float64(delay) * script.demo.pace)
	if scaled <= 0 {
		return nil
	}
	timer := time.NewTimer(scaled)
	defer timer.Stop()
	select {
	case <-script.ctx.Done():
		return script.ctx.Err()
	case <-timer.C:
		return nil
	}
}

// pauseInTurn waits like pause and handles turn actions meanwhile.
func (script *demoScript) pauseInTurn(delay time.Duration, isWanted func(sessionfeed.UserAction) bool) error {
	scaled := time.Duration(float64(delay) * script.demo.pace)
	timer := time.NewTimer(max(scaled, 0))
	defer timer.Stop()
	for {
		select {
		case <-script.ctx.Done():
			return script.ctx.Err()
		case <-timer.C:
			return nil
		case action := <-script.demo.actions:
			if isWanted != nil && isWanted(action) {
				return nil
			}
			if err := script.handleTurnAction(action); err != nil {
				return err
			}
		}
	}
}

// handleTurnAction applies an action that arrives while a turn runs.
func (script *demoScript) handleTurnAction(action sessionfeed.UserAction) error {
	switch typed := action.(type) {
	case sessionfeed.SendUserMessage:
		script.demo.queueMessage(typed.Text)
		return script.sendQueued()
	case sessionfeed.SteerQueuedMessage:
		if !script.demo.removeQueuedMessage(typed.MessageID) {
			return script.notice(sessionfeed.NoticeSeverityInfo, "The message already started a turn.")
		}
		if err := script.sendQueued(); err != nil {
			return err
		}
		return script.notice(sessionfeed.NoticeSeverityInfo, "steered into the running turn")
	case sessionfeed.DeleteQueuedMessage:
		if !script.demo.removeQueuedMessage(typed.MessageID) {
			return script.notice(sessionfeed.NoticeSeverityInfo, "The message already started a turn.")
		}
		return script.sendQueued()
	case sessionfeed.CancelTurn:
		if err := script.send(sessionfeed.CancelRequestAnswered{TurnID: typed.TurnID, Outcome: sessionfeed.CancelOutcomeAccepted}); err != nil {
			return err
		}
		if err := script.pause(300 * time.Millisecond); err != nil {
			return err
		}
		return errDemoTurnCancelled
	case sessionfeed.RunSlashCommand:
		return script.notice(sessionfeed.NoticeSeverityWarning, "/"+typed.Name+" waits until the turn ends in this demo.")
	default:
		script.deferredActions = append(script.deferredActions, action)
		return nil
	}
}

func (demo *DemoSession) queueMessage(text string) {
	demo.mutex.Lock()
	defer demo.mutex.Unlock()
	demo.messageID++
	demo.queued = append(demo.queued, sessionfeed.QueuedMessage{MessageID: fmt.Sprintf("message-%d", demo.messageID), Text: text})
}

func (demo *DemoSession) removeQueuedMessage(messageID string) bool {
	demo.mutex.Lock()
	defer demo.mutex.Unlock()
	for index, message := range demo.queued {
		if message.MessageID == messageID {
			demo.queued = append(demo.queued[:index], demo.queued[index+1:]...)
			return true
		}
	}
	return false
}

func (demo *DemoSession) popQueuedMessage() (sessionfeed.QueuedMessage, bool) {
	demo.mutex.Lock()
	defer demo.mutex.Unlock()
	if len(demo.queued) == 0 {
		return sessionfeed.QueuedMessage{}, false
	}
	first := demo.queued[0]
	demo.queued = demo.queued[1:]
	return first, true
}

func (demo *DemoSession) queuedMessages() []sessionfeed.QueuedMessage {
	demo.mutex.Lock()
	defer demo.mutex.Unlock()
	return append([]sessionfeed.QueuedMessage(nil), demo.queued...)
}

func splitIntoChunks(text string, runesPerChunk int) []string {
	runes := []rune(text)
	chunks := make([]string, 0, len(runes)/runesPerChunk+1)
	for start := 0; start < len(runes); start += runesPerChunk {
		chunks = append(chunks, string(runes[start:min(start+runesPerChunk, len(runes))]))
	}
	return chunks
}
