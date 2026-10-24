// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build webserverzd

package integ

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/integ/webv2approval"
)

// These tests drive a web-only Dex Server whose FlowService target is server-zd.
// web/scripts/run-server-zd-e2e.sh starts that environment.

type dexWebRunSummary struct {
	FlowID     string `json:"flowId"`
	RunID      string `json:"runId"`
	FlowType   string `json:"flowType"`
	FlowStatus string `json:"flowStatus"`
	RunAttempt int32  `json:"runAttempt"`
}

type dexWebRunEvent struct {
	EventID    int64          `json:"eventId"`
	EventTime  string         `json:"eventTime"`
	Type       string         `json:"type"`
	RunAttempt int32          `json:"runAttempt"`
	Payload    map[string]any `json:"payload"`
}

type dexWebRunHistoryPage struct {
	Events              []dexWebRunEvent `json:"events"`
	NextPageToken       string           `json:"nextPageToken"`
	NextInternalEventID int64            `json:"nextInternalEventId"`
}

type dexWebTimeTravelResult struct {
	RunID      string `json:"runId"`
	RunAttempt int32  `json:"runAttempt"`
}

func TestDexWebOnServerZDReadiness(t *testing.T) {
	web := newDexWebOnServerZDClient(t)
	var readiness struct {
		Status string `json:"status"`
	}
	status, err := web.getJSON("/readyz", &readiness)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ready", readiness.Status)
}

func TestDexWebOnServerZDStartsPullFlowsAndFiltersStatus(t *testing.T) {
	web := newDexWebOnServerZDClient(t)
	var catalog struct {
		DefinitionRevision        string `json:"definitionRevision"`
		ServerEngine              string `json:"serverEngine"`
		StartFlowWorkerTargetPull bool   `json:"startFlowWorkerTargetPull"`
	}
	status, err := web.getJSON("/api/v2/catalog", &catalog)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "zd", catalog.ServerEngine)
	require.False(t, catalog.StartFlowWorkerTargetPull, "the pull default must come from the engine, not configuration")
	web.revision = catalog.DefinitionRevision

	approvalFlow := dexWebV2TestFlow{
		flowType:   dex.GetFinalFlowType(&webv2approval.ApprovalFlow{}),
		permission: webv2approval.DecidePermission,
	}
	flowID := newFlowID(t, "web-zd-start")
	// The dialog sends workerTargetPull; this API request omits a target to test the server-zd pull default.
	var started struct {
		RunID string `json:"runId"`
	}
	status, err = web.sendJSON(http.MethodPost, "/api/v2/start", map[string]any{
		"flowType": approvalFlow.flowType, "flowId": flowID, "input": webv2approval.Input{Requester: flowID},
	}, &started)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, started.RunID)

	waitForDexWebEvent(t, web, flowID, started.RunID, "StepWaitForCompleted")
	history := readDexWebHistory(t, web, flowID, started.RunID, 0)
	flowStarted := findDexWebEvent(t, history, "FlowStartedOrContinued")
	workerTarget := flowStarted.Payload["flowConfig"].(map[string]any)["workerTarget"].(map[string]any)
	require.Equal(t, true, workerTarget["isPullMode"], "Start from Dex Web did not use a pull Worker target")
	startContext := findDexWebEvent(t, history, "StepWaitForCompleted").Payload["context"].(map[string]any)
	require.Equal(t, "__start__", startContext["fromStepExecutionId"])

	require.Eventually(t, func() bool {
		v1FlowIDs, v1Err := searchDexWebV1(web, `ExecutionStatus = "Running"`)
		v2FlowIDs, v2Err := searchDexWebV2(web, approvalFlow.flowType, "Running")
		return v1Err == nil && v2Err == nil && slices.Contains(v1FlowIDs, flowID) && slices.Contains(v2FlowIDs, flowID)
	}, dexWebV2ConvergenceTimeout, dexWebV2PollInterval, "the run list never showed %s as Running", flowID)
	// The v2 status filter sends the ExecutionStatus value, not the "Timed out" display label.
	timedOutV1FlowIDs, err := searchDexWebV1(web, `ExecutionStatus = "TimedOut"`)
	require.NoError(t, err)
	require.NotContains(t, timedOutV1FlowIDs, flowID)
	timedOutV2FlowIDs, err := searchDexWebV2(web, approvalFlow.flowType, "TimedOut")
	require.NoError(t, err)
	require.NotContains(t, timedOutV2FlowIDs, flowID)

	web.approveDecision(t, approvalFlow, flowID)
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, flowID, false).Status)
	require.Eventually(t, func() bool {
		completedFlowIDs, searchErr := searchDexWebV2(web, approvalFlow.flowType, "Completed")
		return searchErr == nil && slices.Contains(completedFlowIDs, flowID)
	}, dexWebV2ConvergenceTimeout, dexWebV2PollInterval, "the run list never showed %s as Completed", flowID)
}

func TestDexWebOnServerZDDeletesPendingMessagesAndTimeTravelsRunAttempts(t *testing.T) {
	web := newDexWebOnServerZDClient(t)
	ctx := integrationContext(t)
	flowID := newFlowID(t, "web-zd-debug")
	runID, err := integClient.StartFlow(ctx, webDebugChannelFlow{}, flowID, struct{}{}, dex.StartFlowOptions{})
	require.NoError(t, err)
	waitForDexWebEvent(t, web, flowID, runID, "StepWaitForCompleted")
	summary := readDexWebSummary(t, web, flowID)
	require.Equal(t, runID, summary.RunID)
	require.Equal(t, int32(1), summary.RunAttempt)

	status, err := web.sendJSON(http.MethodPost, "/api/flows/channels/messages", map[string]any{
		"flowId": flowID, "runId": runID, "channelName": webDebugNotes.ChannelName(), "valueType": "string", "value": "note-1",
	}, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	messageID := waitForDexWebPendingMessage(t, web, flowID, runID, webDebugNotes.ChannelName())

	status, err = web.sendJSON(http.MethodDelete, "/api/flows/channels/messages", map[string]any{
		"flowId": flowID, "runId": runID, "channelName": webDebugNotes.ChannelName(), "messageId": messageID,
	}, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	pendingMessages, err := pendingDexWebMessages(web, flowID, runID)
	require.NoError(t, err)
	require.Empty(t, pendingMessages[webDebugNotes.ChannelName()])
	deletion := findDexWebEvent(t, readDexWebHistory(t, web, flowID, runID, 0), "ChannelExternalDelete")
	deleted := deletion.Payload["messages"].([]any)[0].(map[string]any)
	require.Equal(t, webDebugNotes.ChannelName(), deleted["channelName"])
	require.Equal(t, messageID, deleted["messageId"])
	require.Equal(t, int32(1), deletion.RunAttempt)

	firstAttemptHistory := readDexWebHistory(t, web, flowID, runID, 0)
	waitForEvent := findDexWebEvent(t, firstAttemptHistory, "StepWaitForCompleted")
	eventTime, err := time.Parse(time.RFC3339Nano, waitForEvent.EventTime)
	require.NoError(t, err)
	// Dex Web sends an event time typed in the dialog as RFC 3339 UTC with milliseconds.
	historyEventTime := eventTime.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z07:00")
	var byTime dexWebTimeTravelResult
	status, err = web.sendJSON(http.MethodPost, "/api/flows/time-travel", map[string]any{
		"flowId": flowID, "runId": runID, "timeTravelType": 2, "historyEventTime": historyEventTime,
		"reason": "Dex Web event time",
	}, &byTime)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, dexWebTimeTravelResult{RunID: runID, RunAttempt: 2}, byTime)
	require.Equal(t, int32(2), readDexWebSummary(t, web, flowID).RunAttempt)

	currentHistory := readDexWebHistory(t, web, flowID, runID, 0)
	fork := findDexWebEvent(t, currentHistory, "TimeTravelFork")
	require.Equal(t, int32(2), fork.RunAttempt)
	require.Equal(t, float64(1), fork.Payload["previousRunAttempt"])
	firstAttemptLineage := readDexWebHistory(t, web, flowID, runID, 1)
	require.Equal(t, messageID, findDexWebEvent(t, firstAttemptLineage, "ChannelExternalDelete").
		Payload["messages"].([]any)[0].(map[string]any)["messageId"])
	for _, event := range firstAttemptLineage {
		require.Equal(t, int32(1), event.RunAttempt, "attempt 1 lineage contains %s from attempt %d", event.Type, event.RunAttempt)
	}

	stepType := waitForEvent.Payload["context"].(map[string]any)["stepType"].(string)
	var byStep dexWebTimeTravelResult
	status, err = web.sendJSON(http.MethodPost, "/api/flows/time-travel", map[string]any{
		"flowId": flowID, "runId": runID, "timeTravelType": 3, "stepType": stepType, "runAttempt": 1,
		"reason": "Dex Web Step type from an earlier attempt",
	}, &byStep)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, dexWebTimeTravelResult{RunID: runID, RunAttempt: 3}, byStep)
	thirdAttemptFork := findDexWebEvent(t, readDexWebHistory(t, web, flowID, runID, 0), "TimeTravelFork")
	require.Equal(t, int32(3), thirdAttemptFork.RunAttempt)
	require.Equal(t, float64(2), thirdAttemptFork.Payload["previousRunAttempt"])
	require.Equal(t, float64(1), thirdAttemptFork.Payload["targetRunAttempt"])
	waitForDexWebEventInAttempt(t, web, flowID, runID, "StepWaitForCompleted", 3)

	status, err = web.sendJSON(http.MethodPost, "/api/flows/stop", map[string]any{
		"flowId": flowID, "runId": runID, "stopType": int32(dexpb.StopType_STOP_TYPE_TERMINATE), "reason": "Dex Web test finished",
	}, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "Terminated", readDexWebSummary(t, web, flowID).FlowStatus)
}

func newDexWebOnServerZDClient(t *testing.T) *dexWebV2Client {
	t.Helper()
	serverEngine, err := integrationServerEngine()
	require.NoError(t, err)
	require.Equal(t, dexpb.ServerEngine_SERVER_ENGINE_ZD, serverEngine, "run with web/scripts/run-server-zd-e2e.sh")
	return &dexWebV2Client{baseURL: "http://" + dexWebAddress(), httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func readDexWebSummary(t *testing.T, web *dexWebV2Client, flowID string) dexWebRunSummary {
	t.Helper()
	var summary dexWebRunSummary
	status, err := web.getJSON("/api/flows/summary?"+url.Values{"flowId": {flowID}}.Encode(), &summary)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	return summary
}

func readDexWebHistory(t *testing.T, web *dexWebV2Client, flowID string, runID string, runAttempt int32) []dexWebRunEvent {
	t.Helper()
	events, err := tryReadDexWebHistory(web, flowID, runID, runAttempt)
	require.NoError(t, err)
	return events
}

// tryReadDexWebHistory reads the current run attempt's lineage when runAttempt is zero.
func tryReadDexWebHistory(web *dexWebV2Client, flowID string, runID string, runAttempt int32) ([]dexWebRunEvent, error) {
	query := url.Values{"flowId": {flowID}, "runId": {runID}, "startInternalEventId": {"0"}, "estimatePageSize": {"200"}}
	if runAttempt > 0 {
		query.Set("runAttempt", fmt.Sprint(runAttempt))
	}
	events := make([]dexWebRunEvent, 0)
	for {
		var page dexWebRunHistoryPage
		status, err := web.getJSON("/api/flows/history?"+query.Encode(), &page)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("history of %s returned HTTP %d", flowID, status)
		}
		events = append(events, page.Events...)
		if page.NextPageToken == "" {
			return events, nil
		}
		query.Set("nextPageToken", page.NextPageToken)
		query.Set("startInternalEventId", fmt.Sprint(page.NextInternalEventID))
	}
}

func findDexWebEvent(t *testing.T, events []dexWebRunEvent, eventType string) dexWebRunEvent {
	t.Helper()
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == eventType {
			return events[index]
		}
	}
	require.FailNow(t, "history has no event", "%s in %+v", eventType, events)
	return dexWebRunEvent{}
}

func waitForDexWebEvent(t *testing.T, web *dexWebV2Client, flowID string, runID string, eventType string) {
	t.Helper()
	waitForDexWebEventInAttempt(t, web, flowID, runID, eventType, 0)
}

// waitForDexWebEventInAttempt matches any run attempt when runAttempt is zero.
func waitForDexWebEventInAttempt(
	t *testing.T,
	web *dexWebV2Client,
	flowID string,
	runID string,
	eventType string,
	runAttempt int32,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		events, err := tryReadDexWebHistory(web, flowID, runID, 0)
		if err != nil {
			t.Logf("waiting for %s: %v", eventType, err)
			return false
		}
		for _, event := range events {
			if event.Type == eventType && (runAttempt == 0 || event.RunAttempt == runAttempt) {
				return true
			}
		}
		return false
	}, dexWebV2ConvergenceTimeout, dexWebV2PollInterval, "%s never recorded %s", flowID, eventType)
}

func pendingDexWebMessages(web *dexWebV2Client, flowID string, runID string) (map[string][]map[string]any, error) {
	var state struct {
		PendingChannelMessages map[string]struct {
			Messages []map[string]any `json:"messages"`
		} `json:"pendingChannelMessages"`
	}
	status, err := web.getJSON("/api/flows/state?"+url.Values{"flowId": {flowID}, "runId": {runID}}.Encode(), &state)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("state of %s returned HTTP %d", flowID, status)
	}
	messages := make(map[string][]map[string]any, len(state.PendingChannelMessages))
	for channelName, channel := range state.PendingChannelMessages {
		messages[channelName] = channel.Messages
	}
	return messages, nil
}

func waitForDexWebPendingMessage(t *testing.T, web *dexWebV2Client, flowID string, runID string, channelName string) string {
	t.Helper()
	var messageID string
	require.Eventually(t, func() bool {
		pending, err := pendingDexWebMessages(web, flowID, runID)
		if err != nil {
			t.Logf("waiting for a pending %s message: %v", channelName, err)
			return false
		}
		messages := pending[channelName]
		if len(messages) != 1 || !reflect.DeepEqual(messages[0]["value"], map[string]any{"stringValue": "note-1"}) {
			t.Logf("waiting for a pending %s message: %v", channelName, pending)
			return false
		}
		if id, isString := messages[0]["messageId"].(string); isString {
			messageID = id
		}
		return messageID != ""
	}, dexWebV2ConvergenceTimeout, dexWebV2PollInterval, "%s never listed a pending %s message", flowID, channelName)
	return messageID
}

func searchDexWebV1(web *dexWebV2Client, query string) ([]string, error) {
	var result struct {
		Flows []struct {
			FlowID string `json:"flowId"`
		} `json:"flows"`
	}
	status, err := web.sendJSON(http.MethodPost, "/api/flows/search", map[string]any{"query": query, "pageSize": 100}, &result)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("v1 search %s returned HTTP %d", query, status)
	}
	flowIDs := make([]string, 0, len(result.Flows))
	for _, flow := range result.Flows {
		flowIDs = append(flowIDs, flow.FlowID)
	}
	return flowIDs, nil
}

func searchDexWebV2(web *dexWebV2Client, flowType string, executionStatus string) ([]string, error) {
	var result dexWebV2SearchResponse
	status, err := web.sendJSON(http.MethodPost, "/api/v2/search", map[string]any{
		"flowType": flowType, "workQueuePermissions": []string{}, "pageSize": 100,
		"filters": []map[string]any{{"field": "executionStatus", "operator": "eq", "values": []string{executionStatus}}},
	}, &result)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("v2 search for %s returned HTTP %d", executionStatus, status)
	}
	flowIDs := make([]string, 0, len(result.Flows))
	for _, flow := range result.Flows {
		flowIDs = append(flowIDs, flow.FlowID)
	}
	return flowIDs, nil
}
