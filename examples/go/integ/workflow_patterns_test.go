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
	require.Equal(t, []string{"SynchronizeFulfillment"}, completionStepTypes(result))
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
	require.Equal(t, []string{"SynchronizingMerge"}, completionStepTypes(result))
	require.Equal(t,
		[][]string{{"SendEmail", "SendPush"}},
		decodeStepOutputs[[]string](t, result, "SynchronizingMerge"))
}

func TestWorkflowPatternsMultiMerge(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.MultiMergeFlow, "wcp-multi-merge", nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"RecordReview", "RecordReview"}, completionStepTypes(result))
	require.ElementsMatch(t, []string{"LegalReview", "FinanceReview"}, decodeStepOutputs[string](t, result, "RecordReview"))
}

func TestWorkflowPatternsGeneralSynchronizingMerge(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		plan              workflowpatterns.DocumentReviewPlan
		expectedReviews   []string
		isFinanceArchived bool
	}{
		{
			name: "rework adds a review after the split",
			plan: workflowpatterns.DocumentReviewPlan{
				IsLegalCheckNeeded: true, IsFinanceCheckNeeded: true, FinanceReworkRounds: 1, IsFinanceArchived: false,
			},
			expectedReviews: []string{"CheckFinanceTerms-1", "CheckLegalTerms-0", "CheckLegalTerms-1"},
		},
		{
			name: "archived branch does not block the merge",
			plan: workflowpatterns.DocumentReviewPlan{
				IsLegalCheckNeeded: true, IsFinanceCheckNeeded: true, FinanceReworkRounds: 0, IsFinanceArchived: true,
			},
			expectedReviews:   []string{"CheckLegalTerms-0"},
			isFinanceArchived: true,
		},
		{
			name: "merge fires after the last thread",
			plan: workflowpatterns.DocumentReviewPlan{
				IsLegalCheckNeeded: false, IsFinanceCheckNeeded: true, FinanceReworkRounds: 2, IsFinanceArchived: false,
			},
			expectedReviews: []string{"CheckFinanceTerms-2", "CheckLegalTerms-1", "CheckLegalTerms-2"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flowID := startWorkflowPattern(
				t, registry.WorkflowPatterns.GeneralSynchronizingMergeFlow, "wcp-general-synchronizing-merge", testCase.plan)
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t,
				[][]string{testCase.expectedReviews},
				decodeStepOutputs[[]string](t, result, "PublishDocument"))
			expectedStepTypes := []string{"PublishDocument"}
			if testCase.isFinanceArchived {
				expectedStepTypes = append(expectedStepTypes, "ArchiveDocument")
			}
			require.ElementsMatch(t, expectedStepTypes, completionStepTypes(result))
		})
	}
}

func TestWorkflowPatternsStructuredPartialJoin(t *testing.T) {
	flow := registry.WorkflowPatterns.StructuredPartialJoinFlow
	release := func(t *testing.T, flowID string, suppliers ...string) {
		for _, supplier := range suppliers {
			invokeWorkflowPatternRPC(t, flowID, flow.ReleaseSupplierQuote, supplier, nil)
		}
	}
	t.Run("discriminator", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-structured-discriminator",
			workflowpatterns.QuoteRound{RequiredQuotes: 1, Round: 1, RoundCount: 2})
		release(t, flowID, "A")
		waitForWorkflowPatternStepExecution(t, flowID, "PlaceOrder", 1)
		release(t, flowID, "B", "C")
		waitForWorkflowPatternStepExecution(t, flowID, "RequestQuotes", 2)
		release(t, flowID, "B")
		waitForWorkflowPatternStepExecution(t, flowID, "PlaceOrder", 2)
		release(t, flowID, "A", "C")
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []workflowpatterns.AcceptedQuotes{
			{Round: 1, AcceptedQuotes: []string{"A"}},
			{Round: 2, AcceptedQuotes: []string{"B"}},
		}, decodeStepOutputs[workflowpatterns.AcceptedQuotes](t, result, "PlaceOrder"))
		require.Equal(t,
			workflowpatterns.IgnoredQuotes{Round: 1, IgnoredQuotes: []string{"B", "C"}},
			readWorkflowPatternAttribute(t, flowID, workflowpatterns.IgnoredQuotesAttribute))
	})
	t.Run("partial join", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-structured-partial-join",
			workflowpatterns.QuoteRound{RequiredQuotes: 2, Round: 1, RoundCount: 2})
		release(t, flowID, "A")
		waitForWorkflowPatternStepExecution(t, flowID, "QuoteFromSupplierA", 1)
		release(t, flowID, "B")
		waitForWorkflowPatternStepExecution(t, flowID, "PlaceOrder", 1)
		release(t, flowID, "C")
		waitForWorkflowPatternStepExecution(t, flowID, "RequestQuotes", 2)
		release(t, flowID, "C")
		waitForWorkflowPatternStepExecution(t, flowID, "QuoteFromSupplierC", 2)
		release(t, flowID, "A")
		waitForWorkflowPatternStepExecution(t, flowID, "PlaceOrder", 2)
		release(t, flowID, "B")
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []workflowpatterns.AcceptedQuotes{
			{Round: 1, AcceptedQuotes: []string{"A", "B"}},
			{Round: 2, AcceptedQuotes: []string{"A", "C"}},
		}, decodeStepOutputs[workflowpatterns.AcceptedQuotes](t, result, "PlaceOrder"))
		require.Equal(t,
			workflowpatterns.IgnoredQuotes{Round: 1, IgnoredQuotes: []string{"C"}},
			readWorkflowPatternAttribute(t, flowID, workflowpatterns.IgnoredQuotesAttribute))
	})
	t.Run("rejects unknown supplier", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-structured-unknown-supplier",
			workflowpatterns.QuoteRound{RequiredQuotes: 1, Round: 1, RoundCount: 2})
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
		sendSignals(t, flowID, "A")
		require.Equal(t, 1, invokeWorkflowPatternCountRPC(t, flowID, flow.GetQueuedBranchSignalCount))
		sendSignals(t, flowID, "B", "C")
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
		sendSignals(t, flowID, "A", "B")
		require.Equal(t, 2, invokeWorkflowPatternCountRPC(t, flowID, flow.GetQueuedBranchSignalCount))
		sendSignals(t, flowID, "C")
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
	release := func(t *testing.T, flowID string, carriers ...string) {
		for _, carrier := range carriers {
			invokeWorkflowPatternRPC(t, flowID, flow.ReleaseCarrierBid, carrier, nil)
		}
	}
	t.Run("cancelling discriminator", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-cancelling-discriminator", 1)
		release(t, flowID, "B")
		waitForWorkflowPatternStep(t, flowID, "AcceptBids")
		release(t, flowID, "A", "C")
		require.Equal(t, 2, invokeWorkflowPatternCountRPC(t, flowID, flow.GetQueuedCarrierReleaseCount))
		invokeWorkflowPatternRPC(t, flowID, flow.ConfirmCarrierBooking, nil, nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []string{"ConfirmCarrierBooking"}, completionStepTypes(result))
		require.Equal(t, [][]string{{"B"}}, decodeStepOutputs[[]string](t, result, "ConfirmCarrierBooking"))
	})
	t.Run("cancelling partial join", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-cancelling-partial-join", 2)
		release(t, flowID, "A")
		waitForWorkflowPatternStep(t, flowID, "BidFromCarrierA")
		release(t, flowID, "C")
		waitForWorkflowPatternStep(t, flowID, "AcceptBids")
		release(t, flowID, "B")
		require.Equal(t, 1, invokeWorkflowPatternCountRPC(t, flowID, flow.GetQueuedCarrierReleaseCount))
		invokeWorkflowPatternRPC(t, flowID, flow.ConfirmCarrierBooking, nil, nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []string{"ConfirmCarrierBooking"}, completionStepTypes(result))
		require.Equal(t, [][]string{{"A", "C"}}, decodeStepOutputs[[]string](t, result, "ConfirmCarrierBooking"))
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
	require.Equal(t, []string{"MergeParcels"}, completionStepTypes(result))
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
	flow := registry.WorkflowPatterns.DesignTimeMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-design-time", nil)
	pollWorkflowPatternCountRPC(t, flowID, flow.GetStartedDesignTimeReviewCount, 3)
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.OpenDesignTimeReviewGate, 3, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"CollectReviews"}, completionStepTypes(result))
	require.Equal(t,
		[][]string{{"finance", "legal", "security"}},
		decodeStepOutputs[[]string](t, result, "CollectReviews"))
}

func TestWorkflowPatternsRunTimeMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.RunTimeMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-run-time", []string{"dee", "ana", "cy", "ben"})
	pollWorkflowPatternCountRPC(t, flowID, flow.GetStartedRunTimeReviewCount, 4)
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.OpenRunTimeReviewGate, 4, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"CollectReviews"}, completionStepTypes(result))
	require.Equal(t,
		[][]string{{"ana", "ben", "cy", "dee"}},
		decodeStepOutputs[[]string](t, result, "CollectReviews"))
}

func TestWorkflowPatternsNoPriorKnowledgeMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.NoPriorKnowledgeMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-no-prior-knowledge", []string{})
	addSample := func(sampleID string) bool {
		var isAdded bool
		invokeWorkflowPatternRPC(t, flowID, flow.AddSample, sampleID, &isAdded)
		return isAdded
	}
	require.True(t, addSample("s1"))
	require.True(t, addSample("s2"))
	pollWorkflowPatternCountRPC(t, flowID, flow.GetStartedAnalysisCount, 2)
	require.True(t, addSample("s3"))
	pollWorkflowPatternCountRPC(t, flowID, flow.GetStartedAnalysisCount, 3)
	invokeWorkflowPatternRPC(t, flowID, flow.CloseSampleIntake, nil, nil)
	require.False(t, addSample("s4"))
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.OpenSampleGate, 3, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"ReportSamples"}, completionStepTypes(result))
	require.Equal(t, [][]string{{"s1", "s2", "s3"}}, decodeStepOutputs[[]string](t, result, "ReportSamples"))
}

func TestWorkflowPatternsStaticPartialJoinForMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.StaticPartialJoinForMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-static-partial-join",
		workflowpatterns.InspectionRound{InspectionCount: 3, RequiredInspections: 2, Round: 1, RoundCount: 2})
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInspections, 2, nil)
	waitForWorkflowPatternStepExecution(t, flowID, "ApproveBatch", 1)
	requireWorkflowPatternRunning(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInspections, 1, nil)
	waitForWorkflowPatternStepExecution(t, flowID, "StartInspections", 2)
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInspections, 2, nil)
	waitForWorkflowPatternStepExecution(t, flowID, "ApproveBatch", 2)
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseInspections, 1, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	approved := decodeStepOutputs[workflowpatterns.InspectedUnits](t, result, "ApproveBatch")
	require.Len(t, approved, 2)
	for index, batch := range approved {
		require.Equal(t, index+1, batch.Round)
		require.Len(t, batch.UnitIndexes, 2)
		require.NotEqual(t, batch.UnitIndexes[0], batch.UnitIndexes[1])
		require.Subset(t, []int{0, 1, 2}, batch.UnitIndexes)
	}
	ignored := readWorkflowPatternAttribute(t, flowID, workflowpatterns.IgnoredInspectionsAttribute)
	require.Equal(t, 1, ignored.Round)
	require.Len(t, ignored.UnitIndexes, 1)
	require.ElementsMatch(t, []int{0, 1, 2}, append(slices.Clone(approved[0].UnitIndexes), ignored.UnitIndexes...))
}

func TestWorkflowPatternsCancellingPartialJoinForMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.CancellingPartialJoinForMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-cancelling-partial-join",
		workflowpatterns.InspectionPlan{InspectionCount: 3, RequiredInspections: 2})
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseQuorumInspections, 2, nil)
	waitForWorkflowPatternStep(t, flowID, "AcceptQuorumInspections")
	invokeWorkflowPatternRPC(t, flowID, flow.ReleaseQuorumInspections, 1, nil)
	require.Equal(t, 1, invokeWorkflowPatternCountRPC(t, flowID, flow.GetQueuedQuorumReleaseCount))
	invokeWorkflowPatternRPC(t, flowID, flow.CertifyQuorum, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"CertifyQuorum"}, completionStepTypes(result))
	indexes := decodeStepOutputs[[]int](t, result, "CertifyQuorum")[0]
	require.Len(t, indexes, 2)
	require.NotEqual(t, indexes[0], indexes[1])
}

func TestWorkflowPatternsDynamicPartialJoinForMultipleInstances(t *testing.T) {
	flow := registry.WorkflowPatterns.DynamicPartialJoinForMultipleInstancesFlow
	flowID := startWorkflowPattern(t, flow, "wcp-mi-dynamic-partial-join",
		workflowpatterns.BidJoinProgress{RequiredAcceptedBids: 2, AcceptedBids: []int{}})
	addBid := func(amount int) bool {
		var isAdded bool
		invokeWorkflowPatternRPC(t, flowID, flow.AddBid, amount, &isAdded)
		return isAdded
	}
	require.True(t, addBid(500))
	require.True(t, addBid(1500))
	pollWorkflowPatternCountRPC(t, flowID, flow.GetStartedEvaluationCount, 2)
	require.True(t, addBid(700))
	pollWorkflowPatternCountRPC(t, flowID, flow.GetStartedEvaluationCount, 3)
	invokeWorkflowPatternRPC(t, flowID, flow.OpenBidGate, 3, nil)
	pollWorkflowPatternAttribute(t, flowID, workflowpatterns.IsBidAdmissionClosedAttribute, true)
	require.False(t, addBid(400))
	invokeWorkflowPatternRPC(t, flowID, flow.SignContract, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"AwardContract"}, completionStepTypes(result))
	require.Equal(t, [][]int{{500, 700}}, decodeStepOutputs[[]int](t, result, "AwardContract"))
}

func TestWorkflowPatternsDeferredChoice(t *testing.T) {
	flow := registry.WorkflowPatterns.DeferredChoiceFlow
	t.Run("single response", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-deferred-choice", nil)
		invokeWorkflowPatternRPC(t, flowID, flow.PresentOffer, nil, nil)
		waitForWorkflowPatternStep(t, flowID, "PresentOffer")
		invokeWorkflowPatternRPC(t, flowID, flow.DeclineOffer, nil, nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Equal(t, []string{"ArchiveOffer"}, completionStepTypes(result))
		require.Equal(t, []string{"ArchiveOffer"}, decodeStepOutputs[string](t, result, "ArchiveOffer"))
	})
	t.Run("competing responses", func(t *testing.T) {
		flowID := startWorkflowPattern(t, flow, "wcp-deferred-choice-competing", nil)
		invokeWorkflowPatternRPC(t, flowID, flow.AcceptOffer, nil, nil)
		invokeWorkflowPatternRPC(t, flowID, flow.DeclineOffer, nil, nil)
		invokeWorkflowPatternRPC(t, flowID, flow.PresentOffer, nil, nil)
		result := waitForCompletedWorkflowPattern(t, flowID)
		require.Len(t, result.Completions, 1)
		require.Contains(t, []string{"FulfillOffer", "ArchiveOffer"}, result.Completions[0].StepType)
	})
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
	flow := registry.WorkflowPatterns.CancelCaseFlow
	flowID := startWorkflowPattern(t, flow, "wcp-cancel-case", nil)
	waitForWorkflowPatternStep(t, flowID, "StartApplication")
	invokeWorkflowPatternRPC(t, flowID, flow.CancelCase, "applicant withdrew", nil)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowFailed, result.Status, result.ErrorMessage)
	require.NotContains(t, completionStepTypes(result), "ReviewApplication")
	var stoppedChildFlowIDs []string
	_, err := integClient.GetAttributes(integrationContext(t), flowID,
		dex.ReadAttribute(workflowpatterns.StoppedChildFlowIDsAttribute, &stoppedChildFlowIDs))
	require.NoError(t, err)
	require.Len(t, stoppedChildFlowIDs, 1)
	require.Eventually(t, func() bool {
		info, err := integClient.DescribeFlow(integrationContext(t), stoppedChildFlowIDs[0])
		return err == nil && info.Status == dex.FlowCanceled
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "child Flow %s", stoppedChildFlowIDs[0])
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
		err := integClient.InvokeRPC(integrationContext(t), flowID, flow.GetSubmittedPeerReviewCount, nil, &submittedCount)
		return err == nil && submittedCount == 1
	}, workflowPatternWaitTimeout, workflowPatternPollInterval)
	invokeWorkflowPatternRPC(t, flowID, flow.CompletePeerReviewsEarly, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"PublishReviewResults"}, completionStepTypes(result))
	require.Len(t, decodeStepOutputs[[]int](t, result, "PublishReviewResults")[0], 1)
}

func TestWorkflowPatternsArbitraryCycles(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		route            workflowpatterns.SubmissionRoute
		expectedStepType string
		expectedTrail    []string
	}{
		{
			name:             "without draft",
			route:            workflowpatterns.SubmissionRoute{HasDraft: false, ShouldAbandon: false},
			expectedStepType: "PublishArticle",
			expectedTrail: []string{
				"RouteSubmission", "DraftArticle", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle",
			},
		},
		{
			name:             "with draft",
			route:            workflowpatterns.SubmissionRoute{HasDraft: true, ShouldAbandon: false},
			expectedStepType: "PublishArticle",
			expectedTrail:    []string{"RouteSubmission", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle"},
		},
		{
			name:             "abandoned",
			route:            workflowpatterns.SubmissionRoute{HasDraft: false, ShouldAbandon: true},
			expectedStepType: "AbandonArticle",
			expectedTrail:    []string{"RouteSubmission", "DraftArticle", "EditArticle", "DraftArticle", "AbandonArticle"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flowID := startWorkflowPattern(t, registry.WorkflowPatterns.ArbitraryCyclesFlow, "wcp-arbitrary-cycles",
				testCase.route)
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, []string{testCase.expectedStepType}, completionStepTypes(result))
			require.Equal(t,
				[][]string{testCase.expectedTrail},
				decodeStepOutputs[[]string](t, result, testCase.expectedStepType))
		})
	}
}

func TestWorkflowPatternsStructuredLoop(t *testing.T) {
	for _, testCase := range []struct {
		remainingBatches int
		expected         []string
	}{
		{remainingBatches: 3, expected: []string{"batch-3", "batch-2", "batch-1"}},
		{remainingBatches: 0, expected: []string{}},
	} {
		t.Run(strconv.Itoa(testCase.remainingBatches), func(t *testing.T) {
			flowID := startWorkflowPattern(t, registry.WorkflowPatterns.StructuredLoopFlow, "wcp-structured-loop",
				workflowpatterns.BatchProgress{RemainingBatches: testCase.remainingBatches, ProcessedBatches: []string{}})
			result := waitForCompletedWorkflowPattern(t, flowID)
			require.Equal(t, []string{"FinishBatches"}, completionStepTypes(result))
			require.Equal(t, [][]string{testCase.expected}, decodeStepOutputs[[]string](t, result, "FinishBatches"))
		})
	}
}

func TestWorkflowPatternsFactorialRecursion(t *testing.T) {
	flowID := startWorkflowPattern(t, registry.WorkflowPatterns.FactorialRecursionFlow, "wcp-factorial-recursion", 4)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"ComputeFactorial"}, completionStepTypes(result))
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
	sendMotionEvent := func(value int) bool {
		var isDelivered bool
		invokeWorkflowPatternRPC(t, flowID, flow.SendMotionEvent, value, &isDelivered)
		return isDelivered
	}
	require.False(t, sendMotionEvent(1))
	invokeWorkflowPatternRPC(t, flowID, flow.ArmMotionSensor, nil, nil)
	pollWorkflowPatternAttribute(t, flowID, workflowpatterns.IsAwaitingMotionEventAttribute, true)
	require.True(t, sendMotionEvent(2))
	require.False(t, sendMotionEvent(3))
	invokeWorkflowPatternRPC(t, flowID, flow.StopMotionMonitoring, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"AwaitMonitoringStop"}, completionStepTypes(result))
	require.Equal(t, []int{2}, decodeStepOutputs[int](t, result, "AwaitMonitoringStop"))
}

func TestWorkflowPatternsPersistentTrigger(t *testing.T) {
	flow := registry.WorkflowPatterns.PersistentTriggerFlow
	flowID := startWorkflowPattern(t, flow, "wcp-persistent-trigger", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.SendShipmentLabel, "label-1", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.MarkCarrierReady, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"AwaitShipmentLabel"}, completionStepTypes(result))
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

// invokeWorkflowPatternCountRPC returns the output of a query RPC that counts something.
func invokeWorkflowPatternCountRPC(t *testing.T, flowID string, rpc dex.RPC[dex.None, int]) int {
	t.Helper()
	var count int
	invokeWorkflowPatternRPC(t, flowID, rpc, nil, &count)
	return count
}

func pollWorkflowPatternCountRPC(t *testing.T, flowID string, rpc dex.RPC[dex.None, int], expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		var count int
		err := integClient.InvokeRPC(integrationContext(t), flowID, rpc, nil, &count)
		return err == nil && count == expected
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "Flow %s count", flowID)
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
		return err == nil && value == expected
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "Flow %s Attribute %s", flowID, attribute.AttributeName())
}

// readWorkflowPatternAttribute reads the current value of a Flow Attribute.
func readWorkflowPatternAttribute[T any](t *testing.T, flowID string, attribute dex.Attribute[T]) T {
	t.Helper()
	var value T
	_, err := integClient.GetAttributes(integrationContext(t), flowID, dex.ReadAttribute(attribute, &value))
	require.NoError(t, err, "Flow %s Attribute %s", flowID, attribute.AttributeName())
	return value
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
