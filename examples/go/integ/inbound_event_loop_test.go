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
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	inboundeventloop "github.com/superdurable/dex/examples/go/patterns/inbound-event-loop"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestInboundEventLoopRecordsRepliesBeforeTheResolution(t *testing.T) {
	flow := registry.SupportTicket
	flowID := startWorkflowPattern(t, flow, "pattern-inbound-event-loop", "")

	require.ErrorContains(t, integClient.InvokeRPC(integrationContext(t), flowID, flow.SendTicketReply, "  ", nil), "reply text is required")
	require.ErrorContains(t, integClient.InvokeRPC(integrationContext(t), flowID, flow.ResolveTicket, " ", nil), "resolution reason is required")

	invokeWorkflowPatternRPC(t, flowID, flow.SendTicketReply, "printer is offline", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.SendTicketReply, "restarted it, still offline", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.ResolveTicket, "replaced the network cable", nil)

	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"resolved: replaced the network cable"}, decodeStepOutputs[string](t, result, "CloseResolvedTicket"))
	require.Equal(t, []string{"printer is offline", "restarted it, still offline"}, ticketTranscriptTexts(t, flowID))
}

func TestInboundEventLoopCountsReplyLengthInCharacters(t *testing.T) {
	flow := registry.SupportTicket
	flowID := startWorkflowPattern(t, flow, "pattern-inbound-event-loop-length", "")
	// Each emoji is one character but four UTF-8 bytes and two UTF-16 code units.
	longestReply := strings.Repeat("😀", inboundeventloop.MaxReplyLength)

	require.ErrorContains(
		t,
		integClient.InvokeRPC(integrationContext(t), flowID, flow.SendTicketReply, longestReply+"😀", nil),
		"reply text exceeds 4000 characters",
	)
	invokeWorkflowPatternRPC(t, flowID, flow.SendTicketReply, longestReply, nil)
	invokeWorkflowPatternRPC(t, flowID, flow.ResolveTicket, "answered", nil)

	waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{longestReply}, ticketTranscriptTexts(t, flowID))
}

func TestInboundEventLoopClosesAQuietTicket(t *testing.T) {
	flow := registry.SupportTicket
	flowID := startWorkflowPattern(t, flow, "pattern-inbound-event-loop-inactive", "")
	invokeWorkflowPatternRPC(t, flowID, flow.SendTicketReply, "is anyone there?", nil)
	require.Eventually(t, func() bool {
		return slices.Equal([]string{"is anyone there?"}, ticketTranscriptTexts(t, flowID))
	}, workflowPatternWaitTimeout, workflowPatternPollInterval)

	// The reply ended execution 1, so execution 2 holds the inactivity Timer.
	executionNumber, timerIndex := int32(2), int32(0)
	require.Eventually(t, func() bool {
		return integClient.SkipTimer(
			integrationContext(t),
			flowID,
			dex.StepExecutionID{StepType: "AwaitTicketMessage", ExecutionNumber: &executionNumber},
			dex.TimerID{Index: &timerIndex},
		) == nil
	}, workflowPatternWaitTimeout, workflowPatternPollInterval)

	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"closed after inactivity"}, decodeStepOutputs[string](t, result, "CloseInactiveTicket"))
}

// requireInboundEventLoopSmokeTimers checks that the HTTP start sets no Flow timeout and waits on the 24-hour Timer.
func requireInboundEventLoopSmokeTimers(t *testing.T, flowID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		history := runDexcliFlowHistory(t, flowID, "")
		var timerDurations []string
		for _, event := range history.Events {
			switch {
			case event.Type == "FlowStartedOrContinued":
				require.NotContains(t, event.Payload, "flowTimeout", "the HTTP start must not set a Flow timeout")
			case event.Type == "StepWaitForCompleted" && historyEventStepType(event.Payload) == "AwaitTicketMessage":
				timerDurations = append(timerDurations, waitForTimerDurations(event.Payload)...)
			}
		}
		return slices.Equal([]string{"86400"}, timerDurations)
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "AwaitTicketMessage must wait on one 24-hour Timer")
}

func waitForTimerDurations(payload map[string]any) []string {
	output, _ := payload["output"].(map[string]any)
	condition, _ := output["waitForCondition"].(map[string]any)
	timers, _ := condition["timerConditions"].([]any)
	durations := []string{}
	for _, timer := range timers {
		fields, _ := timer.(map[string]any)
		duration, _ := fields["durationSeconds"].(string)
		durations = append(durations, duration)
	}
	return durations
}

func ticketTranscriptTexts(t *testing.T, flowID string) []string {
	t.Helper()
	var chunk inboundeventloop.TranscriptChunk
	_, err := integClient.GetAttributes(
		integrationContext(t),
		flowID,
		dex.ReadAttributeMapInstance(inboundeventloop.TranscriptChunks, inboundeventloop.CurrentTranscriptChunk, &chunk),
	)
	require.NoError(t, err)
	texts := []string{}
	for _, reply := range chunk.Replies {
		texts = append(texts, reply.Text)
	}
	return texts
}
