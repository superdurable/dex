// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// A switched-to session's history prints even when its message sequences
// and turn IDs equal the previous session's.
func TestSwitchedSessionHistoryPrints(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{},
		sessionfeed.UserMessageRecorded{Text: "first session question", TurnID: "turn-1", Sequence: 1},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "first session answer", Sequence: 2},
		sessionfeed.TurnEnded{TurnID: "turn-1"},
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-2"}, sessionfeed.SessionLoaded{},
		sessionfeed.UserMessageRecorded{Text: "second session question", TurnID: "turn-1", Sequence: 1},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "second session answer", Sequence: 2},
	)
	scrollback := harness.scrollbackText()
	require.Contains(t, scrollback, "second session question")
	require.Contains(t, scrollback, "second session answer")
}

var testImage = sessionfeed.ImageAttachment{Name: "screenshot.png", MimeType: "image/png", SizeBytes: 12, Reference: []byte(`{}`)}

func (harness *screenHarness) requestSwitch(request SwitchSessionRequestMsg) {
	_, command := harness.screen.Update(request)
	harness.runCommand(command)
}

// An idle Ctrl-C answers the drop question with "stay" and never counts
// toward the exit.
func TestIdleCtrlCAnswersTheImageDropQuestion(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.ImageAttached{Attachment: testImage})
	harness.requestSwitch(SwitchSessionRequestMsg{SessionID: "openai-sdk-2"})
	require.NotNil(t, harness.screen.pendingImageDrop)
	harness.press(ctrlKey('c'))
	require.Nil(t, harness.screen.pendingImageDrop)
	require.False(t, harness.screen.isIdleExitArmed())
	require.Empty(t, harness.source.recordedActions(), "nothing switches")
	require.Len(t, harness.screen.attachments, 1)
}

// A switch with attached images asks first: n stays and keeps them, y drops
// them and switches.
func TestSwitchWithAttachedImagesAsksFirst(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{},
		sessionfeed.ImageAttached{Attachment: testImage})
	request := SwitchSessionRequestMsg{SessionID: "openai-sdk-2", IsResumed: true}
	harness.requestSwitch(request)
	require.Empty(t, harness.source.recordedActions(), "nothing switches before the answer")
	frame := harness.plainFrame()
	require.Contains(t, frame, "Drop 1 attached image to switch to openai-sdk-2?")
	require.Contains(t, frame, "y drop")
	harness.press(textKey('n'))
	require.Empty(t, harness.source.recordedActions())
	require.Contains(t, harness.scrollbackText(), "staying in this session")
	require.Len(t, harness.screen.attachments, 1, "n keeps the image")

	harness.requestSwitch(request)
	harness.press(textKey('y'))
	require.Equal(t, sessionfeed.SwitchSession{SessionID: "openai-sdk-2", IsResumed: true}, harness.lastAction())
	require.Empty(t, harness.screen.attachments)
}

// Without images a switch goes at once; /new asks the same question.
func TestSwitchWithoutImagesAndNewSessionSwitch(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{})
	harness.requestSwitch(SwitchSessionRequestMsg{SessionID: "openai-sdk-2"})
	require.Equal(t, sessionfeed.SwitchSession{SessionID: "openai-sdk-2"}, harness.lastAction())

	harness = newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{},
		sessionfeed.ImageAttached{Attachment: testImage}, sessionfeed.ImageAttached{Attachment: testImage},
		sessionfeed.SessionStartChanged{IsWaiting: true}, sessionfeed.SessionStartFinished{SessionID: "openai-sdk-3"})
	require.Empty(t, harness.source.recordedActions())
	require.Contains(t, harness.plainFrame(), "Drop 2 attached images to switch to openai-sdk-3?")
	harness.press(textKey('y'))
	require.Equal(t, sessionfeed.SwitchSession{SessionID: "openai-sdk-3"}, harness.lastAction())
}

// targetRecordingSource records each action with the session the screen
// accepted it for.
type targetRecordingSource struct {
	recordingSource
	targets []string
}

func (source *targetRecordingSource) SubmitUserAction(ctx context.Context, action sessionfeed.UserAction) error {
	target, _ := sessionfeed.TargetSessionOf(ctx)
	source.mutex.Lock()
	source.targets = append(source.targets, target)
	source.mutex.Unlock()
	return source.recordingSource.SubmitUserAction(ctx, action)
}

// A switch cancels a queued message of the old session; the in-flight one
// stays marked old.
func TestSwitchCancelsQueuedMessagesOfTheOldSession(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	source := &targetRecordingSource{}
	harness.screen.source = source
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{})
	harness.typeText("first")
	_, firstSend := harness.screen.Update(key(tea.KeyEnter))
	harness.typeText("second")
	_, secondSend := harness.screen.Update(key(tea.KeyEnter))
	require.Nil(t, secondSend, "the second waits for the first")
	require.Contains(t, harness.plainFrame(), "queueing  second")
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-2"}, sessionfeed.SessionLoaded{})
	require.Contains(t, harness.scrollbackText(), "Not sent: the chat switched sessions: second")
	require.NotContains(t, harness.plainFrame(), "queueing  second")
	harness.runCommand(firstSend)
	require.Equal(t, []sessionfeed.UserAction{sessionfeed.SendUserMessage{Text: "first"}}, source.recordedActions())
	require.Equal(t, []string{"openai-sdk-1"}, source.targets)

	harness.typeText("third")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, []string{"openai-sdk-1", "openai-sdk-2"}, source.targets, "a message typed after the switch is for the new session")
	harness.requestSwitch(SwitchSessionRequestMsg{SessionID: "openai-sdk-3"})
	require.Equal(t, "", source.targets[len(source.targets)-1], "a switch is not tied to a session")
}

// An image of the old session that lands late never reaches the new one.
func TestLateImageDoesNotReachTheNewSession(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{})
	harness.requestSwitch(SwitchSessionRequestMsg{SessionID: "openai-sdk-2", IsResumed: true})
	harness.feed(sessionfeed.ImageAttached{Attachment: testImage},
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-2"}, sessionfeed.SessionLoaded{})
	harness.typeText("hello")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.SendUserMessage{Text: "hello"}, harness.lastAction())
}

// A message typed while a relaunched chat replays the left session waits,
// and goes to the launch session once that one loaded.
func TestMessageTypedDuringTheLeftSessionReplayGoesToTheLaunchSession(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100, skipIntro: true})
	source := &targetRecordingSource{}
	harness.screen.source = source
	// The feed replays the left session without SessionLoaded.
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"})
	harness.typeText("typed during the replay")
	harness.press(key(tea.KeyEnter))
	require.Empty(t, source.recordedActions(), "nothing is sent while the left session replays")

	harness.feed(
		sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: "── session openai-sdk-2 ──"},
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-2"},
		sessionfeed.SessionLoaded{},
	)
	require.Equal(t, []sessionfeed.UserAction{sessionfeed.SendUserMessage{Text: "typed during the replay"}}, source.recordedActions())
	require.Equal(t, []string{"openai-sdk-2"}, source.targets)
}
