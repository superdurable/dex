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
	"testing"

	"github.com/stretchr/testify/require"
	cancelcase "github.com/superdurable/dex/examples/go/patterns/cancel-case"
	criticalsection "github.com/superdurable/dex/examples/go/patterns/critical-section"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestDeferredChoiceRoutesOnTheCustomerResponse(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		respond        func(t *testing.T, flowID string)
		expectedStep   string
		expectedOutput string
	}{
		{
			name: "accept",
			respond: func(t *testing.T, flowID string) {
				invokeWorkflowPatternRPC(t, flowID, registry.OfferResponse.AcceptOffer, "looks good", nil)
			},
			expectedStep:   "FulfillOffer",
			expectedOutput: "fulfilled offer-1",
		},
		{
			name: "reject",
			respond: func(t *testing.T, flowID string) {
				invokeWorkflowPatternRPC(t, flowID, registry.OfferResponse.RejectOffer, "too expensive", nil)
			},
			expectedStep:   "CloseRejectedOffer",
			expectedOutput: "rejected offer-1",
		},
		{
			name:           "expire",
			respond:        skipOfferExpiry,
			expectedStep:   "ExpireOffer",
			expectedOutput: "expired offer-1",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flowID := startWorkflowPattern(t, registry.OfferResponse, "pattern-deferred-choice", "offer-1")
			testCase.respond(t, flowID)
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, []string{testCase.expectedStep}, completionStepTypes(result))
			require.Equal(t, []string{testCase.expectedOutput}, decodeStepOutputs[string](t, result, testCase.expectedStep))
		})
	}
}

func TestDeferredChoiceConsumesOnlyTheWinningResponse(t *testing.T) {
	flow := registry.OfferResponse
	flowID := startWorkflowPattern(t, flow, "pattern-deferred-choice-race", "offer-2")
	invokeWorkflowPatternRPC(t, flowID, flow.AcceptOffer, "yes", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.RejectOffer, "no", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Len(t, result.Completions, 1)
	require.Contains(t, []string{"FulfillOffer", "CloseRejectedOffer"}, result.Completions[0].StepType)
}

// skipOfferExpiry retries until the Timer of AwaitOfferResponse is registered.
func skipOfferExpiry(t *testing.T, flowID string) {
	t.Helper()
	timerIndex := int32(0)
	require.Eventually(t, func() bool {
		return integClient.SkipTimer(
			integrationContext(t),
			flowID,
			dex.StepExecutionID{StepType: "AwaitOfferResponse"},
			dex.TimerID{Index: &timerIndex},
		) == nil
	}, workflowPatternWaitTimeout, workflowPatternPollInterval)
}

func TestCriticalSectionRunsOneEditAtATime(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.ContractEdit, "pattern-critical-section", []criticalsection.ContractEdit{
		{Reviewer: "legal"},
		{Reviewer: "finance"},
		{Reviewer: "procurement"},
	})
	records := contractEditRecords(t, flowID)
	require.ElementsMatch(t, []string{"legal", "finance", "procurement"}, contractEditReviewers(records))
	for _, record := range records {
		require.Equal(t, "saved", record.Outcome)
	}
	requireContractEditsDoNotOverlap(t, records)
}

func TestCriticalSectionReturnsTheTokenWhenAnEditFails(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.ContractEdit, "pattern-critical-section-failure", []criticalsection.ContractEdit{
		{Reviewer: "legal", ShouldFailEdit: true},
		{Reviewer: "finance"},
	})
	records := contractEditRecords(t, flowID)
	outcomes := map[string]string{}
	for _, record := range records {
		outcomes[record.Reviewer] = record.Outcome
	}
	require.Equal(t, map[string]string{"legal": "failed", "finance": "saved"}, outcomes)
	requireContractEditsDoNotOverlap(t, records)
}

func TestCancelCaseStopsTheRunningChildFlow(t *testing.T) {
	flow := registry.LoanApplication
	flowID := startWorkflowPattern(t, flow, "pattern-cancel-case", cancelcase.LoanApplication{
		ApplicantID:              "applicant-1",
		CreditReportDelaySeconds: 3600,
	})
	invokeWorkflowPatternRPC(t, flowID, flow.CancelApplication, "applicant withdrew", nil)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowFailed, result.Status, result.ErrorMessage)
	require.Contains(t, result.ErrorMessage, "application canceled: applicant withdrew")
	var childFlowID string
	_, err := integClient.GetAttributes(integrationContext(t), flowID,
		dex.ReadAttribute(cancelcase.StoppedCreditCheckFlowID, &childFlowID))
	require.NoError(t, err)
	require.NotEmpty(t, childFlowID)
	require.Eventually(t, func() bool {
		info, err := integClient.DescribeFlow(integrationContext(t), childFlowID)
		return err == nil && info.Status == dex.FlowCanceled
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "child Flow %s", childFlowID)
}

func TestCancelCaseAfterTheChildFlowFinished(t *testing.T) {
	flow := registry.LoanApplication
	flowID := startWorkflowPattern(t, flow, "pattern-cancel-case-late", cancelcase.LoanApplication{
		ApplicantID:              "applicant-2",
		CreditReportDelaySeconds: 1,
	})
	waitForWorkflowPatternStep(t, flowID, "RunCreditCheck")
	invokeWorkflowPatternRPC(t, flowID, flow.CancelApplication, "applicant withdrew", nil)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowFailed, result.Status, result.ErrorMessage)
	require.Contains(t, result.ErrorMessage, "application canceled: applicant withdrew")
}

func TestCancelCaseCompletesWithTheUnderwriterDecision(t *testing.T) {
	flow := registry.LoanApplication
	flowID := startWorkflowPattern(t, flow, "pattern-cancel-case-decided", cancelcase.LoanApplication{
		ApplicantID:              "applicant-3",
		CreditReportDelaySeconds: 1,
	})
	waitForWorkflowPatternStep(t, flowID, "RunCreditCheck")
	invokeWorkflowPatternRPC(t, flowID, flow.DecideApplication, "approved", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[]string{"approved after credit report for applicant-3"},
		decodeStepOutputs[string](t, result, "AwaitUnderwriterDecision"))
}

func contractEditRecords(t *testing.T, flowID string) []criticalsection.ContractEditRecord {
	t.Helper()
	result := waitForCompletedWorkflowPattern(t, flowID)
	outputs := decodeStepOutputs[[]criticalsection.ContractEditRecord](t, result, "FinishContractEdits")
	require.Len(t, outputs, 1)
	return outputs[0]
}

func contractEditReviewers(records []criticalsection.ContractEditRecord) []string {
	reviewers := make([]string, 0, len(records))
	for _, record := range records {
		reviewers = append(reviewers, record.Reviewer)
	}
	return reviewers
}

func requireContractEditsDoNotOverlap(t *testing.T, records []criticalsection.ContractEditRecord) {
	t.Helper()
	for leftIndex, left := range records {
		for _, right := range records[leftIndex+1:] {
			isDisjoint := left.FinishedAtMillis <= right.StartedAtMillis || right.FinishedAtMillis <= left.StartedAtMillis
			require.True(t, isDisjoint, "%+v overlaps %+v", left, right)
		}
	}
}
