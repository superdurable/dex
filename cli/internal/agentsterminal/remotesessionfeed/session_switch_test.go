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
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/sdk-go/dex"
)

// lockedSessions answers SendUserMessage with a lock conflict until release
// closes, and records the session of every call.
type lockedSessions struct {
	fakeSessions
	release    chan struct{}
	recordLock sync.Mutex
	sessionIDs []string
}

func (sessions *lockedSessions) SendUserMessage(_ context.Context, sessionID string, _ dexagents.AgentUserMessage) (bool, error) {
	sessions.recordLock.Lock()
	sessions.sessionIDs = append(sessions.sessionIDs, sessionID)
	sessions.recordLock.Unlock()
	select {
	case <-sessions.release:
		return true, nil
	default:
		return false, &dex.RPCLockConflictError{ServiceError: &dex.ServiceError{Op: "InvokeRPC", Code: codes.Aborted}}
	}
}

func (sessions *lockedSessions) calledSessions() []string {
	sessions.recordLock.Lock()
	defer sessions.recordLock.Unlock()
	return append([]string(nil), sessions.sessionIDs...)
}

// loopRecorder plays the session loop: it answers local sends and records
// the switches it receives.
type loopRecorder struct {
	mutex    sync.Mutex
	switches []string
}

func (recorder *loopRecorder) serve(feed *RemoteSessionFeed) {
	for input := range feed.inputs {
		switch typed := input.(type) {
		case localSendStarted:
			close(typed.reply)
		case sessionSwitchRequested:
			recorder.mutex.Lock()
			recorder.switches = append(recorder.switches, typed.sessionID)
			recorder.mutex.Unlock()
		}
	}
}

func (recorder *loopRecorder) switched() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]string(nil), recorder.switches...)
}

// A message that meets a lock conflict keeps retrying on its own session,
// and a switch meanwhile waits for it.
func TestSwitchWaitsForWritesOfTheOldSession(t *testing.T) {
	sessions := &lockedSessions{release: make(chan struct{})}
	feed := NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: sessions,
		Requests: noLocalRequests{},
		Launch:   launchcontract.LaunchDocument{Session: launchcontract.LaunchSession{SessionID: "old-session"}},
		Timings:  FeedTimings{LockConflictBudget: 10 * time.Second},
	})
	recorder := &loopRecorder{}
	go recorder.serve(feed)
	sent := make(chan error, 1)
	go func() {
		sent <- feed.SubmitUserAction(context.Background(), sessionfeed.SendUserMessage{Text: "hello"})
	}()
	require.Eventually(t, func() bool { return len(sessions.calledSessions()) >= 1 }, time.Second, 5*time.Millisecond)
	switched := make(chan error, 1)
	go func() {
		switched <- feed.SubmitUserAction(context.Background(), sessionfeed.SwitchSession{SessionID: "new-session", IsResumed: true})
	}()
	require.Eventually(t, func() bool { return len(sessions.calledSessions()) >= 3 }, 5*time.Second, 5*time.Millisecond)
	require.Empty(t, recorder.switched(), "the switch waits for the message")
	require.Equal(t, "old-session", feed.SessionID())
	close(sessions.release)
	require.NoError(t, <-sent)
	require.NoError(t, <-switched)
	for _, sessionID := range sessions.calledSessions() {
		require.Equal(t, "old-session", sessionID, "every retry writes to the session the message began on")
	}
	require.Eventually(t, func() bool { return len(recorder.switched()) == 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, "new-session", feed.SessionID())
	require.NoError(t, feed.SubmitUserAction(context.Background(), sessionfeed.SendUserMessage{Text: "next"}))
	calls := sessions.calledSessions()
	require.Equal(t, "new-session", calls[len(calls)-1], "a later action writes to the new session")
}

// heldImageRequests holds put_image until its context ends.
type heldImageRequests struct {
	noLocalRequests
	started chan struct{}
}

func (requests *heldImageRequests) HasMethod(method string) bool {
	return method == launchcontract.MethodPutImage
}

func (requests *heldImageRequests) CallWithRecoveryKey(ctx context.Context, _ string, _ string, _ any, _ func()) launcherchannel.RequestOutcome {
	close(requests.started)
	<-ctx.Done()
	return launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeUnknown}
}

// A switch cancels the old session's /image, which attaches nothing.
func TestSwitchCancelsImageRequests(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "screenshot.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("\x89PNG test"), 0o600))
	requests := &heldImageRequests{started: make(chan struct{})}
	feed := NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: &fakeSessions{},
		Requests: requests,
		Launch: launchcontract.LaunchDocument{
			Session:      launchcontract.LaunchSession{SessionID: "old-session"},
			Methods:      []string{launchcontract.MethodPutImage},
			Capabilities: launchcontract.LaunchCapabilities{Images: &launchcontract.ImageLimits{MaxPerMessage: 4, MaxBytes: 1 << 20}},
		},
	})
	recorder := &loopRecorder{}
	go recorder.serve(feed)
	attached := make(chan error, 1)
	go func() {
		attached <- feed.SubmitUserAction(context.Background(), sessionfeed.RunSlashCommand{Name: imageCommandName, Argument: imagePath})
	}()
	<-requests.started
	switched := make(chan error, 1)
	go func() {
		switched <- feed.SubmitUserAction(context.Background(), sessionfeed.SwitchSession{SessionID: "new-session"})
	}()
	select {
	case err := <-attached:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the switch did not cancel /image")
	}
	require.NoError(t, <-switched)
	updates := drainFeed(feed)
	require.Empty(t, updatesOfType[sessionfeed.ImageAttached](updates))
	require.Contains(t, noticeTexts(updatesOfType[sessionfeed.NoticeRaised](updates)), "Image not attached: the chat switched sessions.")
	require.Eventually(t, func() bool { return len(recorder.switched()) == 1 }, time.Second, 5*time.Millisecond)
}
