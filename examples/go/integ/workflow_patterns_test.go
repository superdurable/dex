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
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	workflowpatterns "github.com/superdurable/dex/examples/go/patterns/workflow-patterns"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc/codes"
)

const (
	workflowPatternWaitTimeout  = 30 * time.Second
	workflowPatternPollInterval = 50 * time.Millisecond
)

func TestWorkflowPatternsSequence(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.SequenceFlow, "wcp-sequence", []string{})
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"ShipOrder"}, completionStepTypes(result))
	require.Equal(t,
		[][]string{{"ReceiveOrder", "CheckInventory", "ShipOrder"}},
		decodeStepOutputs[[]string](t, result, "ShipOrder"))
}

func TestWorkflowPatternsParallelSplitSynchronization(t *testing.T) {
	flows := registry.WorkflowPatterns
	flowID := startWorkflowPattern(t, flows.ParallelSplitSynchronizationFlow, "wcp-parallel-split", nil)
	waitForWorkflowPatternStep(t, flowID, "PackItems")
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flows.ParallelSplitSynchronizationFlow.ApprovePayment, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[][]string{{"ChargePayment", "PackItems"}},
		decodeStepOutputs[[]string](t, result, "SynchronizeFulfillment"))
}

func TestWorkflowPatternsExclusiveChoiceSimpleMerge(t *testing.T) {
	for _, testCase := range []struct {
		claimAmount   int
		expectedTrail []string
	}{
		{claimAmount: 250, expectedTrail: []string{"RouteClaim", "AutoApprove", "CloseClaim"}},
		{claimAmount: 5000, expectedTrail: []string{"RouteClaim", "ManualReview", "CloseClaim"}},
	} {
		t.Run(strconv.Itoa(testCase.claimAmount), func(t *testing.T) {
			flowID := startWorkflowPattern(
				t, registry.WorkflowPatterns.ExclusiveChoiceSimpleMergeFlow, "wcp-exclusive-choice", testCase.claimAmount)
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, []string{"CloseClaim"}, completionStepTypes(result))
			require.Equal(t, [][]string{testCase.expectedTrail}, decodeStepOutputs[[]string](t, result, "CloseClaim"))
		})
	}
}

func TestWorkflowPatternsMultiChoiceSynchronizingMerge(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.MultiChoiceSynchronizingMergeFlow, "wcp-multi-choice",
		workflowpatterns.NotificationSelection{SendEmail: true, SendSms: false, SendPush: true})
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[][]string{{"SendEmail", "SendPush"}},
		decodeStepOutputs[[]string](t, result, "SynchronizingMerge"))
}

func TestWorkflowPatternsMultiMerge(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.MultiMergeFlow, "wcp-multi-merge", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.ElementsMatch(t, []string{"LegalReview", "FinanceReview"}, decodeStepOutputs[string](t, result, "RecordReview"))
}

func TestWorkflowPatternsGeneralSynchronizingMerge(t *testing.T) {
	flowID := startWorkflowPattern(
		t, registry.WorkflowPatterns.GeneralSynchronizingMergeFlow, "wcp-general-synchronizing-merge", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[][]string{{"ReviewProposal-1", "TranslateProposal-0", "TranslateProposal-1"}},
		decodeStepOutputs[[]string](t, result, "PublishDecision"))
}

func TestWorkflowPatternsStructuredPartialJoin(t *testing.T) {
	flow := registry.WorkflowPatterns.StructuredPartialJoinFlow
	t.Run("discriminator", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-structured-discriminator", 1)
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, "A", nil)
		waitForWorkflowPatternStep(t, flowID, "PlaceOrder")
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, "B", nil)
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, "C", nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, [][]string{{"A"}}, decodeStepOutputs[[]string](t, result, "PlaceOrder"))
		require.Equal(t, [][]string{{"B", "C"}}, decodeStepOutputs[[]string](t, result, "ResetQuoteJoin"))
	})
	t.Run("partial join", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-structured-partial-join", 2)
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, "A", nil)
		waitForWorkflowPatternStep(t, flowID, "QuoteFromSupplierA")
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, "B", nil)
		waitForWorkflowPatternStep(t, flowID, "PlaceOrder")
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, "C", nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, [][]string{{"A", "B"}}, decodeStepOutputs[[]string](t, result, "PlaceOrder"))
		require.Equal(t, [][]string{{"C"}}, decodeStepOutputs[[]string](t, result, "ResetQuoteJoin"))
	})
	t.Run("rejects unknown supplier", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-structured-unknown-supplier", 1)
		err := integClient.InvokeRPC(integrationContext(t), flowID, flow.ReleaseSupplierQuote, "D", nil)
		var invocationErr *dex.WorkerInvocationError
		require.ErrorAs(t, err, &invocationErr)
		require.Equal(t, codes.InvalidArgument, invocationErr.Worker.Code)
		stopWorkflowPattern(t, flowID)
	})
}

func TestWorkflowPatternsBlockingPartialJoin(t *testing.T) {
	flow := registry.WorkflowPatterns.BlockingPartialJoinFlow
	sendSignals := func(t *testing.T, flowID string, branches ...string) {
		for _, branch := range branches {
			invokeWorkflowPatternRPC(t, flowID, flow.SendBranchSignal, branch, nil)
		}
	}
	t.Run("blocking discriminator", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-blocking-discriminator",
			workflowpatterns.BlockingJoinRound{RequiredSignals: 1, Round: 1})
		sendSignals(t, flowID, "A")
		waitForWorkflowPatternStepExecution(t, flowID, "ProcessRound", 1)
		sendSignals(t, flowID, "A", "B", "C")
		waitForWorkflowPatternStepExecution(t, flowID, "ProcessRound", 2)
		sendSignals(t, flowID, "B", "C")
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []workflowpatterns.ProcessedRound{
			{Round: 1, Branches: []string{"A"}},
			{Round: 2, Branches: []string{"A"}},
		}, decodeStepOutputs[workflowpatterns.ProcessedRound](t, result, "ProcessRound"))
	})
	t.Run("blocking partial join", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-blocking-partial-join",
			workflowpatterns.BlockingJoinRound{RequiredSignals: 2, Round: 1})
		sendSignals(t, flowID, "A", "B")
		waitForWorkflowPatternStepExecution(t, flowID, "ProcessRound", 1)
		sendSignals(t, flowID, "A", "B", "C")
		waitForWorkflowPatternStepExecution(t, flowID, "ProcessRound", 2)
		sendSignals(t, flowID, "C")
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []workflowpatterns.ProcessedRound{
			{Round: 1, Branches: []string{"A", "B"}},
			{Round: 2, Branches: []string{"A", "B"}},
		}, decodeStepOutputs[workflowpatterns.ProcessedRound](t, result, "ProcessRound"))
	})
}

func TestWorkflowPatternsCancellingPartialJoin(t *testing.T) {
	flow := registry.WorkflowPatterns.CancellingPartialJoinFlow
	t.Run("cancelling discriminator", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-cancelling-discriminator", 1)
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseCarrierBid, "B", nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []string{"AcceptBids"}, completionStepTypes(result))
		require.Equal(t, [][]string{{"B"}}, decodeStepOutputs[[]string](t, result, "AcceptBids"))
	})
	t.Run("cancelling partial join", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-cancelling-partial-join", 2)
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseCarrierBid, "A", nil)
		waitForWorkflowPatternStep(t, flowID, "BidFromCarrierA")
		invokeWorkflowPatternRPC(t, flowID, flow.ReleaseCarrierBid, "C", nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []string{"AcceptBids"}, completionStepTypes(result))
		require.Equal(t, [][]string{{"A", "C"}}, decodeStepOutputs[[]string](t, result, "AcceptBids"))
	})
}

func TestWorkflowPatternsGeneralizedAndJoin(t *testing.T) {
	flow := registry.WorkflowPatterns.GeneralizedAndJoinFlow
	flowID := startWorkflowPattern(t, flow, "wcp-generalized-and-join",
		workflowpatterns.MatchProgress{ExpectedMatches: 2, MatchedCount: 0})
	invokeWorkflowPatternRPC(t, flowID, flow.DeliverInvoice, "I1", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.DeliverInvoice, "I2", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.DeliverPayment, "P1", nil)
	waitForWorkflowPatternStep(t, flowID, "RecordMatch")
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.DeliverPayment, "P2", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []workflowpatterns.InvoicePaymentMatch{
		{InvoiceID: "I1", PaymentID: "P1"},
		{InvoiceID: "I2", PaymentID: "P2"},
	}, decodeStepOutputs[workflowpatterns.InvoicePaymentMatch](t, result, "RecordMatch"))
}

func TestWorkflowPatternsThreadSplitMerge(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.ThreadSplitMergeFlow, "wcp-thread-split-merge", 3)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, [][]int{{0, 1, 2}}, decodeStepOutputs[[]int](t, result, "MergeParcels"))
}

func TestWorkflowPatternsMultipleInstancesWithoutSynchronization(t *testing.T) {
	flow := registry.WorkflowPatterns.MultipleInstancesWithoutSynchronizationFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-without-sync", []string{"ana", "ben"})
	waitForWorkflowPatternStep(t, flowID, "ConfirmRegistration")
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInvitations, 2, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.ElementsMatch(t, []string{"ana", "ben"}, decodeStepOutputs[string](t, result, "SendInvitation"))
	require.Equal(t, []string{"ConfirmRegistration"}, decodeStepOutputs[string](t, result, "ConfirmRegistration"))
}

func TestWorkflowPatternsDesignTimeMultipleInstances(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.DesignTimeMultipleInstancesFlow, "wcp-mi-design-time", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[][]string{{"finance", "legal", "security"}},
		decodeStepOutputs[[]string](t, result, "CollectReviews"))
}

func TestWorkflowPatternsRunTimeMultipleInstances(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.RunTimeMultipleInstancesFlow, "wcp-mi-run-time",
		[]string{"ana", "ben", "cy", "dee"})
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[][]string{{"ana", "ben", "cy", "dee"}},
		decodeStepOutputs[[]string](t, result, "CollectReviews"))
}

func TestWorkflowPatternsNoPriorKnowledgeMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.NoPriorKnowledgeMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-no-prior-knowledge", []string{})
	for _, sampleID := range []string{"s1", "s2", "s3"} {
		var isAdded bool
		invokeWorkflowPatternRPC(t, flowID, flow.AddSample, sampleID, &isAdded)
		require.True(t, isAdded, sampleID)
	}
	invokeWorkflowPatternRPC(t, flowID, flow.CloseSampleIntake, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, [][]string{{"s1", "s2", "s3"}}, decodeStepOutputs[[]string](t, result, "ReportSamples"))
}

func TestWorkflowPatternsStaticPartialJoinForMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.StaticPartialJoinForMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-static-partial-join",
		workflowpatterns.InspectionPlan{InspectionCount: 3, RequiredInspections: 2})
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInspections, 2, nil)
	waitForWorkflowPatternStep(t, flowID, "ApproveBatch")
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInspections, 1, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	approved := decodeStepOutputs[[]int](t, result, "ApproveBatch")
	reset := decodeStepOutputs[[]int](t, result, "ResetInspectionJoin")
	require.Len(t, approved, 1)
	require.Len(t, reset, 1)
	require.Len(t, approved[0], 2)
	require.Len(t, reset[0], 1)
	require.ElementsMatch(t, []int{0, 1, 2}, append(slices.Clone(approved[0]), reset[0]...))
}

func TestWorkflowPatternsCancellingPartialJoinForMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.CancellingPartialJoinForMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-cancelling-partial-join",
		workflowpatterns.InspectionPlan{InspectionCount: 3, RequiredInspections: 2})
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseQuorumInspections, 2, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"AcceptQuorumInspections"}, completionStepTypes(result))
	indexes := decodeStepOutputs[[]int](t, result, "AcceptQuorumInspections")[0]
	require.Len(t, indexes, 2)
	require.NotEqual(t, indexes[0], indexes[1])
}

func TestWorkflowPatternsDynamicPartialJoinForMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.DynamicPartialJoinForMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-dynamic-partial-join",
		workflowpatterns.BidJoinProgress{RequiredAcceptedBids: 2, AcceptedBids: []int{}})
	for _, amount := range []int{500, 1500, 700} {
		var isAdded bool
		invokeWorkflowPatternRPC(t, flowID, flow.AddBid, amount, &isAdded)
		require.True(t, isAdded, amount)
	}
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, [][]int{{500, 700}}, decodeStepOutputs[[]int](t, result, "AwardContract"))
}

func TestWorkflowPatternsDeferredChoice(t *testing.T) {
	flow := registry.WorkflowPatterns.DeferredChoiceFlow
	for _, testCase := range []struct {
		name             string
		respond          dex.RPC[dex.None, dex.None]
		expectedStepType string
	}{
		{name: "decline", respond: flow.DeclineOffer, expectedStepType: "ArchiveOffer"},
		{name: "accept", respond: flow.AcceptOffer, expectedStepType: "FulfillOffer"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flowID := startWorkflowPattern(t, flow, "wcp-deferred-choice", nil)
			invokeWorkflowPatternRPC(t, flowID, testCase.respond, nil, nil)
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, []string{testCase.expectedStepType}, completionStepTypes(result))
		})
	}
}

func TestWorkflowPatternsInterleavedParallelRouting(t *testing.T) {
	flowID := startWorkflowPattern(
		t, registry.WorkflowPatterns.InterleavedParallelRoutingFlow, "wcp-interleaved-parallel-routing", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	intervals := decodeStepOutputs[[]workflowpatterns.TaskInterval](t, result, "CompleteOnboardingTasks")[0]
	require.ElementsMatch(t,
		[]string{"PrepareContract", "SignContract", "RunCreditCheck"},
		taskIntervalNames(intervals))
	requireTaskIntervalsDoNotOverlap(t, intervals)
	prepare := findTaskInterval(t, intervals, "PrepareContract")
	sign := findTaskInterval(t, intervals, "SignContract")
	require.LessOrEqual(t, prepare.FinishedAtMillis, sign.StartedAtMillis)
}

func TestWorkflowPatternsMilestone(t *testing.T) {
	flow := registry.WorkflowPatterns.MilestoneFlow
	flowID := startWorkflowPattern(t, flow, "wcp-milestone", nil)
	pollWorkflowPatternAttribute(t, flowID, workflowpatterns.IsChangeWindowOpenAttribute, true)
	var isAccepted bool
	invokeWorkflowPatternRPC(t, flowID, flow.RequestConfigChange, "raise-limit", &isAccepted)
	require.True(t, isAccepted)
	invokeWorkflowPatternRPC(t, flowID, flow.ApproveRelease, nil, nil)
	pollWorkflowPatternAttribute(t, flowID, workflowpatterns.IsChangeWindowOpenAttribute, false)
	invokeWorkflowPatternRPC(t, flowID, flow.RequestConfigChange, "late-change", &isAccepted)
	require.False(t, isAccepted)
	invokeWorkflowPatternRPC(t, flowID, flow.FinalizeRelease, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"raise-limit"}, decodeStepOutputs[string](t, result, "ApplyConfigChange"))
}

func TestWorkflowPatternsCriticalSection(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.CriticalSectionFlow, "wcp-critical-section", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	intervals := decodeStepOutputs[[]workflowpatterns.TaskInterval](t, result, "FinishCriticalSections")[0]
	require.ElementsMatch(t, []string{"inventory", "pricing"}, taskIntervalNames(intervals))
	requireTaskIntervalsDoNotOverlap(t, intervals)
}

func TestWorkflowPatternsInterleavedRouting(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.InterleavedRoutingFlow, "wcp-interleaved-routing", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	intervals := decodeStepOutputs[[]workflowpatterns.TaskInterval](t, result, "CompleteAudits")[0]
	require.ElementsMatch(t,
		[]string{"AuditPayroll", "AuditExpenses", "AuditTaxes"},
		taskIntervalNames(intervals))
	requireTaskIntervalsDoNotOverlap(t, intervals)
}

func TestWorkflowPatternsCancelTask(t *testing.T) {
	flow := registry.WorkflowPatterns.CancelTaskFlow
	flowID := startWorkflowPattern(t, flow, "wcp-cancel-task", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.CancelCourierPickup, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"ArrangeSelfDelivery"}, completionStepTypes(result))
}

func TestWorkflowPatternsCancelCase(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.CancelCaseFlow, "wcp-cancel-case", nil)
	require.NoError(t, integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{
		Type:   dex.CancelFlow,
		Reason: "applicant withdrew",
	}))
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCanceled, result.Status, result.ErrorMessage)
	require.Empty(t, result.Completions)
}

func TestWorkflowPatternsCancelRegion(t *testing.T) {
	flow := registry.WorkflowPatterns.CancelRegionFlow
	flowID := startWorkflowPattern(t, flow, "wcp-cancel-region", nil)
	waitForWorkflowPatternStep(t, flowID, "StartApplicationChecks")
	invokeWorkflowPatternRPC(t, flowID, flow.RejectApplication, nil, nil)
	waitForWorkflowPatternStep(t, flowID, "NotifyRejection")
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.CompleteSanctionsScreening, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.ElementsMatch(t, []string{"NotifyRejection", "ScreenSanctions"}, completionStepTypes(result))
}

func TestWorkflowPatternsCancelMultipleInstanceTask(t *testing.T) {
	flow := registry.WorkflowPatterns.CancelMultipleInstanceTaskFlow
	flowID := startWorkflowPattern(t, flow, "wcp-cancel-mi-task", 3)
	waitForWorkflowPatternStep(t, flowID, "StartTranslations")
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseTranslation, nil, nil)
	pollWorkflowPatternAttribute(t, flowID, workflowpatterns.FinishedTranslationCountAttribute, 1)
	invokeWorkflowPatternRPC(t, flowID, flow.CancelTranslations, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Len(t, decodeStepOutputs[int](t, result, "TranslateDocument"), 1)
	require.Equal(t, []int{1}, decodeStepOutputs[int](t, result, "ReportTranslations"))
}

func TestWorkflowPatternsCompleteMultipleInstanceTask(t *testing.T) {
	flow := registry.WorkflowPatterns.CompleteMultipleInstanceTaskFlow
	flowID := startWorkflowPattern(t, flow, "wcp-complete-mi-task", 3)
	waitForWorkflowPatternStep(t, flowID, "StartPeerReviews")
	invokeWorkflowPatternRPC(t, flowID, flow.ReleasePeerReview, nil, nil)
	require.Eventually(t, func() bool {
		var submittedCount int
		invokeWorkflowPatternRPC(t, flowID, flow.GetSubmittedPeerReviewCount, nil, &submittedCount)
		return submittedCount == 1
	}, workflowPatternWaitTimeout, workflowPatternPollInterval)
	invokeWorkflowPatternRPC(t, flowID, flow.CompletePeerReviewsEarly, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"PublishReviewResults"}, completionStepTypes(result))
	require.Len(t, decodeStepOutputs[[]int](t, result, "PublishReviewResults")[0], 1)
}

func TestWorkflowPatternsArbitraryCycles(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		hasDraft      bool
		expectedTrail []string
	}{
		{
			name:     "without draft",
			hasDraft: false,
			expectedTrail: []string{
				"RouteSubmission", "DraftArticle", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle",
			},
		},
		{
			name:          "with draft",
			hasDraft:      true,
			expectedTrail: []string{"RouteSubmission", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flowID := startWorkflowPattern(t, registry.WorkflowPatterns.ArbitraryCyclesFlow, "wcp-arbitrary-cycles",
				workflowpatterns.SubmissionRoute{HasDraft: testCase.hasDraft})
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, [][]string{testCase.expectedTrail}, decodeStepOutputs[[]string](t, result, "PublishArticle"))
		})
	}
}

func TestWorkflowPatternsStructuredLoop(t *testing.T) {
	for _, testCase := range []struct {
		remainingBatches int
		expected         int
	}{
		{remainingBatches: 3, expected: 3},
		{remainingBatches: 0, expected: 0},
	} {
		t.Run(strconv.Itoa(testCase.remainingBatches), func(t *testing.T) {
			flowID := startWorkflowPattern(t, registry.WorkflowPatterns.StructuredLoopFlow, "wcp-structured-loop",
				workflowpatterns.BatchProgress{RemainingBatches: testCase.remainingBatches, ProcessedBatches: 0})
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, []int{testCase.expected}, decodeStepOutputs[int](t, result, "FinishBatches"))
		})
	}
}

func TestWorkflowPatternsFactorialRecursion(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.FactorialRecursionFlow, "wcp-factorial-recursion", 4)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []int{24}, decodeStepOutputs[int](t, result, "ComputeFactorial"))
}

func TestWorkflowPatternsImplicitTermination(t *testing.T) {
	flow := registry.WorkflowPatterns.ImplicitTerminationFlow
	flowID := startWorkflowPattern(t, flow, "wcp-implicit-termination", nil)
	waitForWorkflowPatternStep(t, flowID, "CreateAccount")
	waitForWorkflowPatternStep(t, flowID, "SendWelcomeEmail")
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.CompleteProfile, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.ElementsMatch(t,
		[]string{"CreateAccount", "SendWelcomeEmail", "AwaitProfileCompletion"},
		completionStepTypes(result))
}

func TestWorkflowPatternsExplicitTermination(t *testing.T) {
	flow := registry.WorkflowPatterns.ExplicitTerminationFlow
	flowID := startWorkflowPattern(t, flow, "wcp-explicit-termination", nil)
	waitForWorkflowPatternStep(t, flowID, "StartIncident")
	invokeWorkflowPatternRPC(t, flowID, flow.ResolveIncident, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"ResolveIncident"}, completionStepTypes(result))
}

func TestWorkflowPatternsTransientTrigger(t *testing.T) {
	flow := registry.WorkflowPatterns.TransientTriggerFlow
	flowID := startWorkflowPattern(t, flow, "wcp-transient-trigger", nil)
	var isDelivered bool
	invokeWorkflowPatternRPC(t, flowID, flow.SendMotionEvent, 1, &isDelivered)
	require.False(t, isDelivered)
	invokeWorkflowPatternRPC(t, flowID, flow.ArmMotionSensor, nil, nil)
	pollWorkflowPatternAttribute(t, flowID, workflowpatterns.IsAwaitingMotionEventAttribute, true)
	invokeWorkflowPatternRPC(t, flowID, flow.SendMotionEvent, 2, &isDelivered)
	require.True(t, isDelivered)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []int{2}, decodeStepOutputs[int](t, result, "AwaitMotionEvent"))
}

func TestWorkflowPatternsPersistentTrigger(t *testing.T) {
	flow := registry.WorkflowPatterns.PersistentTriggerFlow
	flowID := startWorkflowPattern(t, flow, "wcp-persistent-trigger", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.SendShipmentLabel, "label-1", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.MarkCarrierReady, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"label-1"}, decodeStepOutputs[string](t, result, "AwaitShipmentLabel"))
}

func startWorkflowPattern(t *testing.T, flow dex.Flow, prefix string, input any) string {
	t.Helper()
	flowID := newFlowID(t, prefix)
	_, err := integClient.StartFlow(integrationContext(t), flow, flowID, input, dex.StartFlowOptions{})
	require.NoError(t, err)
	return flowID
}

func waitForWorkflowPatternStep(t *testing.T, flowID string, stepType string) {
	t.Helper()
	waitForWorkflowPatternStepExecution(t, flowID, stepType, 1)
}

func waitForWorkflowPatternStepExecution(t *testing.T, flowID string, stepType string, executionNumber int32) {
	t.Helper()
	err := integClient.WaitForStepCompletion(
		integrationContext(t),
		flowID,
		dex.StepExecutionID{StepType: stepType, ExecutionNumber: &executionNumber},
		dex.WaitForStepCompletionOptions{RequestTimeout: workflowPatternWaitTimeout},
	)
	require.NoError(t, err, "Flow %s Step %s-%d", flowID, stepType, executionNumber)
}

func requireWorkflowPatternRunning(t *testing.T, flowID string) {
	t.Helper()
	info, err := integClient.DescribeFlow(integrationContext(t), flowID)
	require.NoError(t, err)
	require.Equal(t, dex.FlowRunning, info.Status, "Flow %s", flowID)
}

// invokeWorkflowPatternRPC retries an RPC that lost an Attribute lock to a Step;
// a conflicting invocation never commits.
func invokeWorkflowPatternRPC(t *testing.T, flowID string, rpc any, input any, output any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), workflowPatternWaitTimeout)
	defer cancel()
	for {
		err := integClient.InvokeRPC(ctx, flowID, rpc, input, output)
		var lockConflict *dex.RPCLockConflictError
		if !errors.As(err, &lockConflict) {
			require.NoError(t, err, "Flow %s", flowID)
			return
		}
		select {
		case <-ctx.Done():
			require.NoError(t, err, "Flow %s: lock conflict did not clear", flowID)
		case <-time.After(workflowPatternPollInterval):
		}
	}
}

func pollWorkflowPatternAttribute[T comparable](
	t *testing.T,
	flowID string,
	attribute dex.Attribute[T],
	expected T,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		var value T
		_, err := integClient.GetAttributes(integrationContext(t), flowID, dex.ReadAttribute(attribute, &value))
		require.NoError(t, err)
		return value == expected
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "Flow %s Attribute %s", flowID, attribute.AttributeName())
}

func stopWorkflowPattern(t *testing.T, flowID string) {
	t.Helper()
	require.NoError(t, integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{
		Type:   dex.TerminateFlow,
		Reason: "test finished",
	}))
}

func waitForCompletedWorkflowPattern(t *testing.T, flowID string) dex.FlowResult {
	t.Helper()
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status, "Flow %s: %s", flowID, result.ErrorMessage)
	return result
}

func completionStepTypes(result dex.FlowResult) []string {
	stepTypes := make([]string, 0, len(result.Completions))
	for _, completion := range result.Completions {
		stepTypes = append(stepTypes, completion.StepType)
	}
	return stepTypes
}

// decodeStepOutputs decodes the outputs of stepType in Step execution order.
func decodeStepOutputs[T any](t *testing.T, result dex.FlowResult, stepType string) []T {
	t.Helper()
	completions := []dex.StepCompletion{}
	for _, completion := range result.Completions {
		if completion.StepType == stepType {
			completions = append(completions, completion)
		}
	}
	slices.SortFunc(completions, func(left, right dex.StepCompletion) int {
		return stepExecutionNumber(t, left.StepExecutionID) - stepExecutionNumber(t, right.StepExecutionID)
	})
	outputs := make([]T, 0, len(completions))
	for _, completion := range completions {
		var output T
		require.NoError(t, completion.Output.Decode(&output))
		outputs = append(outputs, output)
	}
	return outputs
}

func stepExecutionNumber(t *testing.T, stepExecutionID string) int {
	t.Helper()
	separator := strings.LastIndex(stepExecutionID, "-")
	require.GreaterOrEqual(t, separator, 0, "Step execution ID %q", stepExecutionID)
	executionNumber, err := strconv.Atoi(stepExecutionID[separator+1:])
	require.NoError(t, err, "Step execution ID %q", stepExecutionID)
	return executionNumber
}

func taskIntervalNames(intervals []workflowpatterns.TaskInterval) []string {
	names := make([]string, 0, len(intervals))
	for _, interval := range intervals {
		names = append(names, interval.Task)
	}
	return names
}

func findTaskInterval(
	t *testing.T,
	intervals []workflowpatterns.TaskInterval,
	task string,
) workflowpatterns.TaskInterval {
	t.Helper()
	index := slices.IndexFunc(intervals, func(interval workflowpatterns.TaskInterval) bool {
		return interval.Task == task
	})
	require.GreaterOrEqual(t, index, 0, "task %s", task)
	return intervals[index]
}

func requireTaskIntervalsDoNotOverlap(t *testing.T, intervals []workflowpatterns.TaskInterval) {
	t.Helper()
	for leftIndex, left := range intervals {
		for _, right := range intervals[leftIndex+1:] {
			isDisjoint := left.FinishedAtMillis <= right.StartedAtMillis || right.FinishedAtMillis <= left.StartedAtMillis
			require.True(t, isDisjoint, "%+v overlaps %+v", left, right)
		}
	}
}
