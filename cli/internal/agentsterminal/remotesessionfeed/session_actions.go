// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The slash commands the feed runs.
const (
	modelCommandName      = "model"
	imageCommandName      = "image"
	newCommandName        = "new"
	webCommandName        = "web"
	connectorsCommandName = "connectors"
)

// maxLockRetryDelay caps the pause between lock conflict retries.
const maxLockRetryDelay = time.Second

func (feed *RemoteSessionFeed) runAction(ctx context.Context, action sessionfeed.UserAction) error {
	switch typed := action.(type) {
	case sessionfeed.SendUserMessage:
		return feed.sendUserMessage(ctx, typed)
	case sessionfeed.ResolveApproval:
		var reason *string
		if !typed.IsApproved && strings.TrimSpace(typed.Reason) != "" {
			reason = &typed.Reason
		}
		resolution := sessionfeed.InterruptApproved
		if !typed.IsApproved {
			resolution = sessionfeed.InterruptRejected
		}
		return feed.resolveInterrupt(ctx, typed.InterruptID, resolution, func(callContext context.Context, sessionID string) (bool, error) {
			return feed.sessions.ResolveInterrupt(callContext, sessionID, typed.InterruptID, typed.IsApproved, reason)
		})
	case sessionfeed.AnswerQuestion:
		return feed.resolveInterrupt(ctx, typed.InterruptID, sessionfeed.InterruptAnswered, func(callContext context.Context, sessionID string) (bool, error) {
			return feed.sessions.AnswerQuestion(callContext, sessionID, typed.InterruptID, typed.Answer)
		})
	case sessionfeed.DeclineQuestion:
		return feed.resolveInterrupt(ctx, typed.InterruptID, sessionfeed.InterruptDeclined, func(callContext context.Context, sessionID string) (bool, error) {
			return feed.sessions.ResolveInterrupt(callContext, sessionID, typed.InterruptID, false, nil)
		})
	case sessionfeed.CancelTurn:
		feed.cancelTurn(ctx, typed.TurnID)
		return nil
	case sessionfeed.SteerQueuedMessage:
		return feed.changeQueuedMessage(ctx, "steer", func(callContext context.Context, sessionID string) (bool, error) {
			messageID := typed.MessageID
			return feed.sessions.SteerUserMessage(callContext, sessionID, dexagents.SteerUserMessageRequest{MessageID: &messageID})
		})
	case sessionfeed.DeleteQueuedMessage:
		return feed.changeQueuedMessage(ctx, "delete", func(callContext context.Context, sessionID string) (bool, error) {
			return feed.sessions.DeleteQueuedMessage(callContext, sessionID, typed.MessageID)
		})
	case sessionfeed.ExecutePlan:
		return feed.executePlan(ctx, typed.Revision)
	case sessionfeed.RunSlashCommand:
		return feed.runSlashCommand(ctx, typed)
	case sessionfeed.MoveSessionStartToBackground:
		// The screen already decided; the request goes on, and no cancel is sent.
		feed.notice(sessionfeed.NoticeSeverityInfo, "The new session keeps starting; this chat goes on meanwhile.")
		return nil
	case sessionfeed.SwitchSession:
		return feed.postInput(ctx, sessionSwitchRequested{sessionID: typed.SessionID})
	}
	return fmt.Errorf("remotesessionfeed: unknown action %T", action)
}

// postInput hands an input to the session loop.
func (feed *RemoteSessionFeed) postInput(ctx context.Context, input loopInput) error {
	select {
	case feed.inputs <- input:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// callWithLockRetries retries call while a Step or another RPC holds one of
// its locks, for at most LockConflictBudget. Interrupts often meet a lock at
// first, while the turn's Step still runs.
func (feed *RemoteSessionFeed) callWithLockRetries(ctx context.Context, call func(context.Context, string) (bool, error)) (bool, error) {
	deadline := time.Now().Add(feed.timings.LockConflictBudget)
	delay := 50 * time.Millisecond
	for {
		callContext, cancel := context.WithTimeout(ctx, feed.timings.RPCTimeout)
		isAccepted, err := call(callContext, feed.SessionID())
		cancel()
		var lockConflict *dex.RPCLockConflictError
		if !errors.As(err, &lockConflict) {
			return isAccepted, err
		}
		if time.Now().Add(delay).After(deadline) {
			return false, fmt.Errorf("the session stayed locked for %s: %w", feed.timings.LockConflictBudget, err)
		}
		if !sleepFor(ctx, delay) {
			return false, ctx.Err()
		}
		delay = min(delay*2, maxLockRetryDelay)
	}
}

func (feed *RemoteSessionFeed) sendUserMessage(ctx context.Context, message sessionfeed.SendUserMessage) error {
	reply := make(chan struct{})
	if err := feed.postInput(ctx, localSendStarted{text: message.Text, reply: reply}); err != nil {
		return err
	}
	select {
	case <-reply:
	case <-ctx.Done():
		return ctx.Err()
	}
	feed.stateMutex.Lock()
	feed.attachedImages = 0
	feed.stateMutex.Unlock()
	userMessage := dexagents.AgentUserMessage{Text: message.Text, PlanMode: message.IsPlanMode}
	for _, attachment := range message.Images {
		var image dexagents.AgentImageReference
		if err := json.Unmarshal(attachment.Reference, &image); err != nil {
			return fmt.Errorf("read the attached image %s: %w", attachment.Name, err)
		}
		userMessage.Images = append(userMessage.Images, image)
	}
	isQueued, err := feed.callWithLockRetries(ctx, func(callContext context.Context, sessionID string) (bool, error) {
		return feed.sessions.SendUserMessage(callContext, sessionID, userMessage)
	})
	if err != nil || !isQueued {
		if forgetErr := feed.postInput(ctx, localSendRejected{text: message.Text}); forgetErr != nil {
			return forgetErr
		}
	}
	if err != nil {
		return errors.New(describeError(err))
	}
	if !isQueued {
		feed.emit(sessionfeed.UserMessageRejected{Text: message.Text, Reason: rejectedMessageReason(message)})
		return nil
	}
	return feed.postInput(ctx, refreshRequested{})
}

func rejectedMessageReason(message sessionfeed.SendUserMessage) string {
	switch {
	case len(message.Images) > 0:
		return "this agent does not take these images. Send the message again without them."
	case message.IsPlanMode:
		return "this agent has no plan mode. Send the message again without /plan."
	}
	return "the session did not take the message. Press Up to edit it and send again."
}

func (feed *RemoteSessionFeed) resolveInterrupt(
	ctx context.Context,
	interruptID string,
	resolution sessionfeed.InterruptResolution,
	call func(context.Context, string) (bool, error),
) error {
	if err := feed.postInput(ctx, interruptResolving{interruptID: interruptID, resolution: resolution}); err != nil {
		return err
	}
	isResolved, err := feed.callWithLockRetries(ctx, call)
	if err != nil || !isResolved {
		if forgetErr := feed.postInput(ctx, interruptResolving{interruptID: interruptID, isForgotten: true}); forgetErr != nil {
			return forgetErr
		}
	}
	if err != nil {
		return errors.New(describeError(err))
	}
	if !isResolved {
		feed.notice(sessionfeed.NoticeSeverityInfo, "It is no longer pending; the session record shows what happened.")
		return feed.postInput(ctx, refreshRequested{})
	}
	return nil
}

// cancelTurn reports the answer only from the RPC result and the snapshot:
// false means no matching turn runs, or the turn refuses turn control.
func (feed *RemoteSessionFeed) cancelTurn(ctx context.Context, turnID string) {
	answer := sessionfeed.CancelRequestAnswered{TurnID: turnID}
	isAccepted, err := feed.callWithLockRetries(ctx, func(callContext context.Context, sessionID string) (bool, error) {
		return feed.sessions.CancelTurn(callContext, sessionID, &turnID)
	})
	switch {
	case err != nil:
		answer.Outcome = sessionfeed.CancelOutcomeNotConfirmed
		answer.Reason = describeError(err)
	case isAccepted:
		answer.Outcome = sessionfeed.CancelOutcomeAccepted
		if postErr := feed.postInput(ctx, cancelAccepted{turnID: turnID}); postErr != nil {
			return
		}
	default:
		answer.Outcome = feed.refusedCancelOutcome(ctx, turnID)
	}
	feed.emit(answer)
}

func (feed *RemoteSessionFeed) refusedCancelOutcome(ctx context.Context, turnID string) sessionfeed.CancelOutcome {
	snapshotContext, cancel := context.WithTimeout(ctx, feed.timings.SnapshotTimeout)
	defer cancel()
	snapshot, err := feed.sessions.GetAgentSnapshot(snapshotContext, feed.SessionID())
	if err != nil || snapshot.State == nil {
		return sessionfeed.CancelOutcomeRefused
	}
	if snapshot.State.ActiveTurnID == nil || *snapshot.State.ActiveTurnID != turnID {
		return sessionfeed.CancelOutcomeTurnAlreadyEnded
	}
	return sessionfeed.CancelOutcomeRefused
}

func (feed *RemoteSessionFeed) changeQueuedMessage(ctx context.Context, verb string, call func(context.Context, string) (bool, error)) error {
	isChanged, err := feed.callWithLockRetries(ctx, call)
	var notFound *dex.ChannelMessageNotFoundError
	switch {
	case errors.As(err, &notFound):
		feed.notice(sessionfeed.NoticeSeverityInfo, "The message already started a turn.")
	case err != nil:
		return errors.New(describeError(err))
	case !isChanged && verb == "steer":
		feed.notice(sessionfeed.NoticeSeverityWarning, "Cannot steer the message now; it stays queued.")
	case !isChanged:
		feed.notice(sessionfeed.NoticeSeverityInfo, "The message already left the queue.")
	}
	return feed.postInput(ctx, refreshRequested{})
}

func (feed *RemoteSessionFeed) executePlan(ctx context.Context, revision int64) error {
	isAccepted, err := feed.callWithLockRetries(ctx, func(callContext context.Context, sessionID string) (bool, error) {
		return feed.sessions.ExecutePlan(callContext, sessionID, revision)
	})
	if err != nil {
		return errors.New(describeError(err))
	}
	if !isAccepted {
		feed.notice(sessionfeed.NoticeSeverityWarning, "The plan cannot run now: it changed, it runs already, or the session is busy.")
	}
	return feed.postInput(ctx, refreshRequested{})
}

func (feed *RemoteSessionFeed) runSlashCommand(ctx context.Context, command sessionfeed.RunSlashCommand) error {
	switch command.Name {
	case modelCommandName:
		return feed.changeModel(ctx, command.Argument)
	case imageCommandName:
		feed.actionMutex.Lock()
		defer feed.actionMutex.Unlock()
		feed.attachImage(ctx, command.Argument)
		return nil
	case newCommandName:
		return feed.startSession(ctx)
	case webCommandName:
		feed.actionMutex.Lock()
		defer feed.actionMutex.Unlock()
		feed.openSessionInDexWeb(ctx)
		return nil
	case connectorsCommandName:
		feed.actionMutex.Lock()
		defer feed.actionMutex.Unlock()
		feed.listConnectors(ctx)
		return nil
	}
	return fmt.Errorf("/%s is not a command of this session", command.Name)
}

func (feed *RemoteSessionFeed) changeModel(ctx context.Context, model string) error {
	snapshotContext, cancel := context.WithTimeout(ctx, feed.timings.SnapshotTimeout)
	snapshot, err := feed.sessions.GetAgentSnapshot(snapshotContext, feed.SessionID())
	cancel()
	if err != nil {
		return errors.New(describeError(err))
	}
	if model == "" {
		current, _ := snapshot.SessionSettings["model"].(string)
		if current == "" {
			current = "the command's default"
		}
		feed.notice(sessionfeed.NoticeSeverityInfo, "The model is "+current+". Change it with /model ID.")
		return nil
	}
	settings := maps.Clone(snapshot.SessionSettings)
	if settings == nil {
		settings = map[string]any{}
	}
	settings["model"] = model
	isStored, err := feed.callWithLockRetries(ctx, func(callContext context.Context, sessionID string) (bool, error) {
		return feed.sessions.UpdateSessionSettings(callContext, sessionID, settings)
	})
	if err != nil {
		return errors.New(describeError(err))
	}
	if !isStored {
		feed.notice(sessionfeed.NoticeSeverityWarning, "The session did not take the model yet; try again after it loads.")
		return nil
	}
	feed.notice(sessionfeed.NoticeSeverityInfo, "The next turn uses "+model+".")
	return feed.postInput(ctx, refreshRequested{})
}

// attachImage checks the file locally, then stores it through put_image.
func (feed *RemoteSessionFeed) attachImage(ctx context.Context, argument string) {
	limits := feed.launch.Capabilities.Images
	if limits == nil {
		feed.notice(sessionfeed.NoticeSeverityError, "This agent takes no images.")
		return
	}
	if !feed.requests.HasMethod(launchcontract.MethodPutImage) {
		feed.notice(sessionfeed.NoticeSeverityWarning, "/image is not available with this command version.")
		return
	}
	path, err := feed.imagePath(argument)
	if err != nil {
		feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: "+err.Error()+".")
		return
	}
	name := filepath.Base(path)
	feed.stateMutex.Lock()
	attached := feed.attachedImages
	feed.stateMutex.Unlock()
	if int64(attached) >= limits.MaxPerMessage {
		feed.notice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Image not attached: a message carries at most %d images.", limits.MaxPerMessage))
		return
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: "+path+" does not exist.")
		return
	case err != nil:
		feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: "+err.Error()+".")
		return
	case !info.Mode().IsRegular():
		feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: "+path+" is not a regular file.")
		return
	case info.Size() > limits.MaxBytes:
		feed.notice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Image not attached: %s is %s; the limit is %s.", name, formatSize(info.Size()), formatSize(limits.MaxBytes)))
		return
	}
	key, err := launchcontract.NewIdempotencyKey()
	if err != nil {
		feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: "+err.Error()+".")
		return
	}
	outcome := feed.requests.CallWithRecoveryKey(ctx, launchcontract.MethodPutImage, key, map[string]any{"path": path}, nil)
	attachment, isAttached := feed.imageAttachment(outcome, name)
	if !isAttached {
		return
	}
	feed.stateMutex.Lock()
	feed.attachedImages++
	feed.stateMutex.Unlock()
	feed.emit(sessionfeed.ImageAttached{Attachment: attachment})
}

func (feed *RemoteSessionFeed) imageAttachment(outcome launcherchannel.RequestOutcome, name string) (sessionfeed.ImageAttachment, bool) {
	switch outcome.Kind {
	case launcherchannel.OutcomeDone:
		var result struct {
			Image json.RawMessage `json:"image"`
			Name  string          `json:"name"`
		}
		if launchcontract.CheckMethodResultJSON(launchcontract.MethodPutImage, outcome.Result) != nil ||
			json.Unmarshal(outcome.Result, &result) != nil {
			feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: the command returned an invalid image reference.")
			return sessionfeed.ImageAttachment{}, false
		}
		var image dexagents.AgentImageReference
		if err := json.Unmarshal(result.Image, &image); err != nil {
			feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: the command returned an invalid image reference.")
			return sessionfeed.ImageAttachment{}, false
		}
		return sessionfeed.ImageAttachment{Name: result.Name, MimeType: image.MimeType, SizeBytes: image.SizeBytes, Reference: result.Image}, true
	case launcherchannel.OutcomeFailed:
		if feed.isUnknownMethod(outcome, launchcontract.MethodPutImage, "/image") {
			return sessionfeed.ImageAttachment{}, false
		}
		feed.notice(sessionfeed.NoticeSeverityError, "Image not attached: "+outcome.Error.Message)
		return sessionfeed.ImageAttachment{}, false
	}
	feed.notice(sessionfeed.NoticeSeverityWarning, "Could not attach "+name+"; try again.")
	return sessionfeed.ImageAttachment{}, false
}

// isUnknownMethod turns a feature off after unknown_method and says so.
func (feed *RemoteSessionFeed) isUnknownMethod(outcome launcherchannel.RequestOutcome, method string, command string) bool {
	if outcome.Error == nil || outcome.Error.Code != launchcontract.ErrorCodeUnknownMethod {
		return false
	}
	feed.requests.DisableMethod(method)
	feed.notice(sessionfeed.NoticeSeverityWarning, command+" is not available with this command version.")
	return true
}

// imagePath removes one level of quotes, expands ~, and makes the path
// absolute.
func (feed *RemoteSessionFeed) imagePath(argument string) (string, error) {
	path := strings.TrimSpace(argument)
	if len(path) >= 2 && (path[0] == '"' || path[0] == '\'') && path[len(path)-1] == path[0] {
		path = path[1 : len(path)-1]
	}
	if path == "" {
		return "", errors.New("usage: /image PATH")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if feed.home == "" {
			return "", errors.New("~ needs HOME")
		}
		path = filepath.Join(feed.home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(feed.workDir, path)
	}
	return filepath.Clean(path), nil
}

// startSession starts a session through the launcher and reports it with
// SessionStartFinished. The screen decides on its event loop whether to
// switch at once or ask, because only it knows whether Esc moved the wait to
// the background. No cancel is ever sent for it.
func (feed *RemoteSessionFeed) startSession(ctx context.Context) error {
	if !feed.requests.HasMethod(launchcontract.MethodStartSession) {
		feed.notice(sessionfeed.NoticeSeverityWarning, "/new is not available with this command version.")
		return nil
	}
	if !feed.isStartingSession.CompareAndSwap(false, true) {
		feed.notice(sessionfeed.NoticeSeverityInfo, "A new session is already starting.")
		return nil
	}
	defer feed.isStartingSession.Store(false)
	key, err := launchcontract.NewIdempotencyKey()
	if err != nil {
		return err
	}
	feed.emit(sessionfeed.SessionStartChanged{IsWaiting: true})
	feed.notice(sessionfeed.NoticeSeverityInfo, "starting a session… Esc keeps it starting in the background")
	outcome := feed.requests.CallWithRecoveryKey(ctx, launchcontract.MethodStartSession, key, map[string]any{}, func() {
		feed.notice(sessionfeed.NoticeSeverityInfo, "still starting…")
	})
	if sessionID, isStarted := feed.startedSessionID(key, outcome); isStarted {
		// The screen reads it while its wait state still says how to go on.
		feed.emit(sessionfeed.SessionStartFinished{SessionID: sessionID})
	}
	feed.emit(sessionfeed.SessionStartChanged{IsWaiting: false})
	return nil
}

// startedSessionID returns the session a start_session outcome started, or
// shows why there is none.
func (feed *RemoteSessionFeed) startedSessionID(key string, outcome launcherchannel.RequestOutcome) (string, bool) {
	switch outcome.Kind {
	case launcherchannel.OutcomeDone:
		var result struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(outcome.Result, &result) != nil || !launchcontract.IsSessionIDOfKey(feed.launch.Command.Harness, key, result.SessionID) {
			feed.notice(sessionfeed.NoticeSeverityError, "Could not confirm the new session; check /sessions.")
			return "", false
		}
		return result.SessionID, true
	case launcherchannel.OutcomeFailed:
		switch {
		case feed.isUnknownMethod(outcome, launchcontract.MethodStartSession, "/new"):
		case outcome.Error.Code == "id_conflict":
			feed.notice(sessionfeed.NoticeSeverityWarning, "Session ID conflict; press /new again.")
		default:
			feed.notice(sessionfeed.NoticeSeverityError, "Could not start a session: "+outcome.Error.Message)
		}
		return "", false
	}
	feed.notice(sessionfeed.NoticeSeverityWarning, "Could not confirm the new session; check /sessions.")
	return "", false
}

func (feed *RemoteSessionFeed) openSessionInDexWeb(ctx context.Context) {
	webURL := feed.launch.WebURL()
	if webURL == "" || !feed.requests.HasMethod(launchcontract.MethodOpenURL) {
		feed.notice(sessionfeed.NoticeSeverityWarning, "/web is not available with this command version.")
		return
	}
	target := strings.TrimRight(webURL, "/") + "/v1/flows/" + url.PathEscape(feed.SessionID())
	if !launchcontract.IsOpenURLAllowed(webURL, target) {
		feed.notice(sessionfeed.NoticeSeverityError, "Cannot open this session in Dex Web.")
		return
	}
	key, err := launchcontract.NewIdempotencyKey()
	if err != nil {
		feed.notice(sessionfeed.NoticeSeverityError, err.Error())
		return
	}
	outcome := feed.requests.Call(ctx, launchcontract.MethodOpenURL, key, map[string]any{"url": target})
	switch outcome.Kind {
	case launcherchannel.OutcomeDone:
		feed.notice(sessionfeed.NoticeSeverityInfo, "opened "+target)
	case launcherchannel.OutcomeFailed:
		if !feed.isUnknownMethod(outcome, launchcontract.MethodOpenURL, "/web") {
			feed.notice(sessionfeed.NoticeSeverityError, "Dex Web did not open: "+outcome.Error.Message+" Open "+target+" yourself.")
		}
	default:
		feed.notice(sessionfeed.NoticeSeverityWarning, "Dex Web may not have opened. Open "+target+" yourself.")
	}
}

func (feed *RemoteSessionFeed) listConnectors(ctx context.Context) {
	if !feed.requests.HasMethod(launchcontract.MethodListConnectors) {
		feed.notice(sessionfeed.NoticeSeverityWarning, "/connectors is not available with this command version.")
		return
	}
	key, err := launchcontract.NewIdempotencyKey()
	if err != nil {
		feed.notice(sessionfeed.NoticeSeverityError, err.Error())
		return
	}
	outcome := feed.requests.Call(ctx, launchcontract.MethodListConnectors, key, map[string]any{})
	switch outcome.Kind {
	case launcherchannel.OutcomeDone:
		feed.showConnectors(outcome.Result)
	case launcherchannel.OutcomeFailed:
		if !feed.isUnknownMethod(outcome, launchcontract.MethodListConnectors, "/connectors") {
			feed.notice(sessionfeed.NoticeSeverityError, "Connectors not listed: "+outcome.Error.Message)
		}
	default:
		feed.notice(sessionfeed.NoticeSeverityWarning, "Connectors not listed: the command did not answer. Try /connectors again.")
	}
}

func (feed *RemoteSessionFeed) showConnectors(result json.RawMessage) {
	var list struct {
		Tools []struct {
			Name      string `json:"name"`
			IsEnabled bool   `json:"is_enabled"`
		} `json:"tools"`
	}
	if launchcontract.CheckMethodResultJSON(launchcontract.MethodListConnectors, result) != nil ||
		json.Unmarshal(result, &list) != nil {
		feed.notice(sessionfeed.NoticeSeverityError, "Connectors not listed: the command returned an invalid list.")
		return
	}
	if len(list.Tools) == 0 {
		feed.notice(sessionfeed.NoticeSeverityInfo, "No connector tools. Add a connection in Dex Web, then try /connectors again.")
		return
	}
	var enabled, disabled []string
	for _, tool := range list.Tools {
		if tool.IsEnabled {
			enabled = append(enabled, tool.Name)
		} else {
			disabled = append(disabled, tool.Name)
		}
	}
	text := fmt.Sprintf("%d connector tools: %s", len(enabled), strings.Join(enabled, ", "))
	if len(disabled) > 0 {
		text += fmt.Sprintf("; %d not enabled: %s", len(disabled), strings.Join(disabled, ", "))
	}
	feed.notice(sessionfeed.NoticeSeverityInfo, text)
}
