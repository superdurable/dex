/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Client;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.patterns.workflowpatterns.ArbitraryCyclesFlow;
import io.superdurable.dex.patterns.workflowpatterns.BlockingPartialJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelCaseFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelMultipleInstanceTaskFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelRegionFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelTaskFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancellingPartialJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancellingPartialJoinForMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.CompleteMultipleInstanceTaskFlow;
import io.superdurable.dex.patterns.workflowpatterns.CriticalSectionFlow;
import io.superdurable.dex.patterns.workflowpatterns.DeferredChoiceFlow;
import io.superdurable.dex.patterns.workflowpatterns.DesignTimeMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.DynamicPartialJoinForMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.ExclusiveChoiceSimpleMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.ExplicitTerminationFlow;
import io.superdurable.dex.patterns.workflowpatterns.FactorialRecursionFlow;
import io.superdurable.dex.patterns.workflowpatterns.GeneralSynchronizingMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.GeneralizedAndJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.ImplicitTerminationFlow;
import io.superdurable.dex.patterns.workflowpatterns.InterleavedParallelRoutingFlow;
import io.superdurable.dex.patterns.workflowpatterns.InterleavedRoutingFlow;
import io.superdurable.dex.patterns.workflowpatterns.MilestoneFlow;
import io.superdurable.dex.patterns.workflowpatterns.MultiChoiceSynchronizingMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.MultiMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.MultipleInstancesWithoutSynchronizationFlow;
import io.superdurable.dex.patterns.workflowpatterns.NoPriorKnowledgeMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.ParallelSplitSynchronizationFlow;
import io.superdurable.dex.patterns.workflowpatterns.PersistentTriggerFlow;
import io.superdurable.dex.patterns.workflowpatterns.RunTimeMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.SequenceFlow;
import io.superdurable.dex.patterns.workflowpatterns.StaticPartialJoinForMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.StructuredLoopFlow;
import io.superdurable.dex.patterns.workflowpatterns.StructuredPartialJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.TaskInterval;
import io.superdurable.dex.patterns.workflowpatterns.ThreadSplitMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.TransientTriggerFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.function.Supplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Asserts the defining behavior of every Workflow Patterns (WCP) example. */
@ExtendWith(SharedIntegExtension.class)
public class WorkflowPatternsIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    private static final long LOCK_CONFLICT_RETRY_MILLIS = 100L;

    // ---- basic control flow ----

    @Test
    void sequenceRunsStepsOneAfterAnother() {
        final String flowId = startPatternFlow(SequenceFlow.class, "sequence", new String[0]);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("ShipOrder"), stepTypesOf(completions));
        assertEquals(
                List.of("ReceiveOrder", "CheckInventory", "ShipOrder"),
                stringsOf(completions.get(0)));
    }

    @Test
    void parallelSplitAndSynchronizationWaitForEveryBranch() {
        final String flowId =
                startPatternFlow(ParallelSplitSynchronizationFlow.class, "parallel-split", null);
        waitForStep(flowId, "PackItems", 1);
        assertRunning(flowId);
        final ParallelSplitSynchronizationFlow stub =
                rpcStub(ParallelSplitSynchronizationFlow.class, flowId);
        client().invokeRPC(stub::approvePayment);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("SynchronizeFulfillment"), stepTypesOf(completions));
        assertEquals(List.of("ChargePayment", "PackItems"), stringsOf(completions.get(0)));
    }

    @Test
    void exclusiveChoiceAndSimpleMergeRunExactlyOneBranch() {
        final String autoFlowId =
                startPatternFlow(ExclusiveChoiceSimpleMergeFlow.class, "exclusive-auto", 250);
        final List<StepCompletion> autoCompletions = waitForCompletedFlow(autoFlowId);
        assertEquals(List.of("CloseClaim"), stepTypesOf(autoCompletions));
        assertEquals(
                List.of("RouteClaim", "AutoApprove", "CloseClaim"), stringsOf(autoCompletions.get(0)));

        final String manualFlowId =
                startPatternFlow(ExclusiveChoiceSimpleMergeFlow.class, "exclusive-manual", 5000);
        final List<StepCompletion> manualCompletions = waitForCompletedFlow(manualFlowId);
        assertEquals(List.of("CloseClaim"), stepTypesOf(manualCompletions));
        assertEquals(
                List.of("RouteClaim", "ManualReview", "CloseClaim"),
                stringsOf(manualCompletions.get(0)));
    }

    // ---- multi-choice and merges ----

    @Test
    void multiChoiceSynchronizingMergeWaitsOnlyForChosenBranches() {
        final String flowId = startPatternFlow(
                MultiChoiceSynchronizingMergeFlow.class,
                "multi-choice",
                new MultiChoiceSynchronizingMergeFlow.NotificationSelection(true, false, true));
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("SynchronizingMerge"), stepTypesOf(completions));
        assertEquals(List.of("SendEmail", "SendPush"), stringsOf(completions.get(0)));
    }

    @Test
    void multiMergeRunsTheMergedStepOncePerArrival() {
        final String flowId = startPatternFlow(MultiMergeFlow.class, "multi-merge", null);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("RecordReview", "RecordReview"), stepTypesOf(completions));
        assertEquals(
                Set.of("LegalReview", "FinanceReview"),
                new HashSet<String>(outputsOf(completions, "RecordReview", String.class)));
    }

    @Test
    void generalSynchronizingMergeIncludesAReviewAddedByRework() {
        final List<StepCompletion> completions = runGeneralSynchronizingMerge(
                "general-merge-rework",
                new GeneralSynchronizingMergeFlow.DocumentReviewPlan(true, true, 1, false));
        assertEquals(List.of("PublishDocument"), stepTypesOf(completions));
        assertEquals(
                List.of("CheckFinanceTerms-1", "CheckLegalTerms-0", "CheckLegalTerms-1"),
                stringsOf(completions.get(0)));
    }

    @Test
    void generalSynchronizingMergeIsNotBlockedByAnArchivedBranch() {
        final List<StepCompletion> completions = runGeneralSynchronizingMerge(
                "general-merge-archive",
                new GeneralSynchronizingMergeFlow.DocumentReviewPlan(true, true, 0, true));
        assertEquals(List.of("ArchiveDocument", "PublishDocument"), stepTypesOf(completions));
        assertEquals(
                List.of(List.of("CheckLegalTerms-0")),
                outputsOf(completions, "PublishDocument", String[].class).stream()
                        .map(List::of)
                        .toList());
    }

    @Test
    void generalSynchronizingMergeFiresOnceAfterTheLastReachableThread() {
        final List<StepCompletion> completions = runGeneralSynchronizingMerge(
                "general-merge-last",
                new GeneralSynchronizingMergeFlow.DocumentReviewPlan(false, true, 2, false));
        assertEquals(List.of("PublishDocument"), stepTypesOf(completions));
        assertEquals(
                List.of("CheckFinanceTerms-2", "CheckLegalTerms-1", "CheckLegalTerms-2"),
                stringsOf(completions.get(0)));
    }

    // ---- partial joins ----

    @Test
    void structuredDiscriminatorRunsTwoFullJoinCycles() {
        final String flowId = startPatternFlow(
                StructuredPartialJoinFlow.class,
                "discriminator",
                new StructuredPartialJoinFlow.QuoteRound(1, 1, 2));
        releaseSupplierQuotes(flowId, "A");
        waitForStep(flowId, "PlaceOrder", 1);
        releaseSupplierQuotes(flowId, "B", "C");
        waitForStep(flowId, "RequestQuotes", 2);
        releaseSupplierQuotes(flowId, "B");
        waitForStep(flowId, "PlaceOrder", 2);
        releaseSupplierQuotes(flowId, "A", "C");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of(
                        new StructuredPartialJoinFlow.AcceptedQuotes(1, List.of("A")),
                        new StructuredPartialJoinFlow.AcceptedQuotes(2, List.of("B"))),
                outputsInExecutionOrder(
                        completions, "PlaceOrder", StructuredPartialJoinFlow.AcceptedQuotes.class));
        assertEquals(
                new StructuredPartialJoinFlow.IgnoredQuotes(1, List.of("B", "C")),
                attributeValue(flowId, StructuredPartialJoinFlow.ignoredQuotes));
    }

    @Test
    void structuredPartialJoinRunsTwoFullJoinCycles() {
        final String flowId = startPatternFlow(
                StructuredPartialJoinFlow.class,
                "partial-join",
                new StructuredPartialJoinFlow.QuoteRound(2, 1, 2));
        releaseSupplierQuotes(flowId, "A");
        waitForStep(flowId, "QuoteFromSupplierA", 1);
        releaseSupplierQuotes(flowId, "B");
        waitForStep(flowId, "PlaceOrder", 1);
        releaseSupplierQuotes(flowId, "C");
        waitForStep(flowId, "RequestQuotes", 2);
        releaseSupplierQuotes(flowId, "C");
        waitForStep(flowId, "QuoteFromSupplierC", 2);
        releaseSupplierQuotes(flowId, "A");
        waitForStep(flowId, "PlaceOrder", 2);
        releaseSupplierQuotes(flowId, "B");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of(
                        new StructuredPartialJoinFlow.AcceptedQuotes(1, List.of("A", "B")),
                        new StructuredPartialJoinFlow.AcceptedQuotes(2, List.of("A", "C"))),
                outputsInExecutionOrder(
                        completions, "PlaceOrder", StructuredPartialJoinFlow.AcceptedQuotes.class));
        assertEquals(
                new StructuredPartialJoinFlow.IgnoredQuotes(1, List.of("C")),
                attributeValue(flowId, StructuredPartialJoinFlow.ignoredQuotes));
    }

    @Test
    void blockingDiscriminatorHoldsANextRoundSignalUntilTheJoinResets() {
        final String flowId = startPatternFlow(
                BlockingPartialJoinFlow.class,
                "blocking-discriminator",
                new BlockingPartialJoinFlow.BlockingJoinRound(1, 1));
        final BlockingPartialJoinFlow stub = rpcStub(BlockingPartialJoinFlow.class, flowId);
        sendBranchSignals(flowId, "A");
        waitForStep(flowId, "ProcessRound", 1);
        sendBranchSignals(flowId, "A");
        assertEquals(1, client().invokeRPC(stub::getQueuedBranchSignalCount));
        sendBranchSignals(flowId, "B", "C");
        waitForStep(flowId, "ProcessRound", 2);
        sendBranchSignals(flowId, "B", "C");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of(
                        new BlockingPartialJoinFlow.ProcessedRound(1, List.of("A")),
                        new BlockingPartialJoinFlow.ProcessedRound(2, List.of("A"))),
                outputsInExecutionOrder(
                        completions, "ProcessRound", BlockingPartialJoinFlow.ProcessedRound.class));
    }

    @Test
    void blockingPartialJoinHoldsNextRoundSignalsUntilTheJoinResets() {
        final String flowId = startPatternFlow(
                BlockingPartialJoinFlow.class,
                "blocking-partial-join",
                new BlockingPartialJoinFlow.BlockingJoinRound(2, 1));
        final BlockingPartialJoinFlow stub = rpcStub(BlockingPartialJoinFlow.class, flowId);
        sendBranchSignals(flowId, "A", "B");
        waitForStep(flowId, "ProcessRound", 1);
        sendBranchSignals(flowId, "A", "B");
        assertEquals(2, client().invokeRPC(stub::getQueuedBranchSignalCount));
        sendBranchSignals(flowId, "C");
        waitForStep(flowId, "ProcessRound", 2);
        sendBranchSignals(flowId, "C");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of(
                        new BlockingPartialJoinFlow.ProcessedRound(1, List.of("A", "B")),
                        new BlockingPartialJoinFlow.ProcessedRound(2, List.of("A", "B"))),
                outputsInExecutionOrder(
                        completions, "ProcessRound", BlockingPartialJoinFlow.ProcessedRound.class));
    }

    @Test
    void cancellingDiscriminatorCancelsTheOtherCarriersAfterTheFirstBid() {
        final String flowId =
                startPatternFlow(CancellingPartialJoinFlow.class, "cancelling-discriminator", 1);
        final CancellingPartialJoinFlow stub = rpcStub(CancellingPartialJoinFlow.class, flowId);
        client().invokeRPC(stub::releaseCarrierBid, "B");
        waitForStep(flowId, "AcceptBids", 1);
        client().invokeRPC(stub::releaseCarrierBid, "A");
        client().invokeRPC(stub::releaseCarrierBid, "C");
        assertEquals(2, client().invokeRPC(stub::getQueuedCarrierReleaseCount));
        client().invokeRPC(stub::confirmCarrierBooking);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("ConfirmCarrierBooking"), stepTypesOf(completions));
        assertEquals(List.of("B"), stringsOf(completions.get(0)));
    }

    @Test
    void cancellingPartialJoinCancelsTheLastCarrierAfterTwoBids() {
        final String flowId =
                startPatternFlow(CancellingPartialJoinFlow.class, "cancelling-partial-join", 2);
        final CancellingPartialJoinFlow stub = rpcStub(CancellingPartialJoinFlow.class, flowId);
        client().invokeRPC(stub::releaseCarrierBid, "A");
        waitForStep(flowId, "BidFromCarrierA", 1);
        client().invokeRPC(stub::releaseCarrierBid, "C");
        waitForStep(flowId, "AcceptBids", 1);
        client().invokeRPC(stub::releaseCarrierBid, "B");
        assertEquals(1, client().invokeRPC(stub::getQueuedCarrierReleaseCount));
        client().invokeRPC(stub::confirmCarrierBooking);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("ConfirmCarrierBooking"), stepTypesOf(completions));
        assertEquals(List.of("A", "C"), stringsOf(completions.get(0)));
    }

    // ---- and-join and threads ----

    @Test
    void generalizedAndJoinKeepsAnExtraInvoiceForTheNextFiring() {
        final String flowId = startPatternFlow(
                GeneralizedAndJoinFlow.class,
                "and-join",
                new GeneralizedAndJoinFlow.MatchProgress(2, 0));
        final GeneralizedAndJoinFlow stub = rpcStub(GeneralizedAndJoinFlow.class, flowId);
        client().invokeRPC(stub::deliverInvoice, "I1");
        client().invokeRPC(stub::deliverInvoice, "I2");
        client().invokeRPC(stub::deliverPayment, "P1");
        waitForStep(flowId, "RecordMatch", 1);
        assertRunning(flowId);
        client().invokeRPC(stub::deliverPayment, "P2");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of(
                        new GeneralizedAndJoinFlow.InvoicePaymentMatch("I1", "P1"),
                        new GeneralizedAndJoinFlow.InvoicePaymentMatch("I2", "P2")),
                outputsInExecutionOrder(
                        completions, "RecordMatch", GeneralizedAndJoinFlow.InvoicePaymentMatch.class));
    }

    @Test
    void threadSplitAndThreadMergeMergeEveryParcelThread() {
        final String flowId = startPatternFlow(ThreadSplitMergeFlow.class, "threads", 3);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("MergeParcels"), stepTypesOf(completions));
        assertEquals(List.of(0, 1, 2), integersOf(completions.get(0)));
    }

    // ---- multiple instances ----

    @Test
    void multipleInstancesWithoutSynchronizationContinueWithoutWaiting() {
        final String flowId = startPatternFlow(
                MultipleInstancesWithoutSynchronizationFlow.class,
                "instances-unsynchronized",
                new String[] {"ana", "ben"});
        waitForStep(flowId, "ConfirmRegistration", 1);
        assertRunning(flowId);
        final MultipleInstancesWithoutSynchronizationFlow stub =
                rpcStub(MultipleInstancesWithoutSynchronizationFlow.class, flowId);
        client().invokeRPC(stub::releaseInvitations, 2);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        final List<String> invited = outputsOf(completions, "SendInvitation", String.class);
        assertEquals(2, invited.size());
        assertEquals(Set.of("ana", "ben"), new HashSet<String>(invited));
        assertEquals(
                List.of("ConfirmRegistration"),
                outputsOf(completions, "ConfirmRegistration", String.class));
    }

    @Test
    void designTimeMultipleInstancesRunAllThreeReviewsAtOnce() throws InterruptedException {
        final String flowId =
                startPatternFlow(DesignTimeMultipleInstancesFlow.class, "instances-design", null);
        final DesignTimeMultipleInstancesFlow stub =
                rpcStub(DesignTimeMultipleInstancesFlow.class, flowId);
        awaitRpcValue(
                () -> client().invokeRPC(stub::getStartedDesignTimeReviewCount), 3, "started reviews");
        assertRunning(flowId);
        client().invokeRPC(stub::openDesignTimeReviewGate, 3);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("CollectReviews"), stepTypesOf(completions));
        assertEquals(List.of("finance", "legal", "security"), stringsOf(completions.get(0)));
    }

    @Test
    void runTimeMultipleInstancesRunEveryReviewerAtOnce() throws InterruptedException {
        final String flowId = startPatternFlow(
                RunTimeMultipleInstancesFlow.class,
                "instances-runtime",
                new String[] {"dee", "ana", "cy", "ben"});
        final RunTimeMultipleInstancesFlow stub = rpcStub(RunTimeMultipleInstancesFlow.class, flowId);
        awaitRpcValue(
                () -> client().invokeRPC(stub::getStartedRunTimeReviewCount), 4, "started reviews");
        assertRunning(flowId);
        client().invokeRPC(stub::openRunTimeReviewGate, 4);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("CollectReviews"), stepTypesOf(completions));
        assertEquals(List.of("ana", "ben", "cy", "dee"), stringsOf(completions.get(0)));
    }

    @Test
    void noPriorKnowledgeMultipleInstancesAddInstancesWhileOthersRun() throws InterruptedException {
        final String flowId = startPatternFlow(
                NoPriorKnowledgeMultipleInstancesFlow.class, "instances-open", new String[0]);
        final NoPriorKnowledgeMultipleInstancesFlow stub =
                rpcStub(NoPriorKnowledgeMultipleInstancesFlow.class, flowId);
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::addSample, "s1")));
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::addSample, "s2")));
        awaitRpcValue(() -> client().invokeRPC(stub::getStartedAnalysisCount), 2, "started analyses");
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::addSample, "s3")));
        awaitRpcValue(() -> client().invokeRPC(stub::getStartedAnalysisCount), 3, "started analyses");
        retryOnLockConflict(() -> {
            client().invokeRPC(stub::closeSampleIntake);
            return true;
        });
        assertFalse(retryOnLockConflict(() -> client().invokeRPC(stub::addSample, "s4")));
        assertRunning(flowId);
        client().invokeRPC(stub::openSampleGate, 3);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("ReportSamples"), stepTypesOf(completions));
        assertEquals(List.of("s1", "s2", "s3"), stringsOf(completions.get(0)));
    }

    @Test
    void staticPartialJoinForMultipleInstancesRunsTwoFullJoinCycles() {
        final String flowId = startPatternFlow(
                StaticPartialJoinForMultipleInstancesFlow.class,
                "instances-static-join",
                new StaticPartialJoinForMultipleInstancesFlow.InspectionRound(3, 2, 1, 2));
        final StaticPartialJoinForMultipleInstancesFlow stub =
                rpcStub(StaticPartialJoinForMultipleInstancesFlow.class, flowId);
        client().invokeRPC(stub::releaseInspections, 2);
        waitForStep(flowId, "ApproveBatch", 1);
        assertRunning(flowId);
        client().invokeRPC(stub::releaseInspections, 1);
        waitForStep(flowId, "StartInspections", 2);
        client().invokeRPC(stub::releaseInspections, 2);
        waitForStep(flowId, "ApproveBatch", 2);
        client().invokeRPC(stub::releaseInspections, 1);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        final List<StaticPartialJoinForMultipleInstancesFlow.InspectedUnits> approved =
                outputsInExecutionOrder(
                        completions,
                        "ApproveBatch",
                        StaticPartialJoinForMultipleInstancesFlow.InspectedUnits.class);
        final StaticPartialJoinForMultipleInstancesFlow.InspectedUnits ignored = attributeValue(
                flowId, StaticPartialJoinForMultipleInstancesFlow.ignoredInspections);
        assertEquals(2, approved.size());
        for (int round = 1; round <= 2; round++) {
            final List<Integer> indexes = approved.get(round - 1).unitIndexes();
            assertEquals(round, approved.get(round - 1).round());
            assertEquals(2, new HashSet<Integer>(indexes).size());
            assertTrue(Set.of(0, 1, 2).containsAll(indexes));
        }
        assertEquals(1, ignored.round());
        assertEquals(1, ignored.unitIndexes().size());
        final Set<Integer> roundOneIndexes = new HashSet<Integer>(approved.get(0).unitIndexes());
        roundOneIndexes.addAll(ignored.unitIndexes());
        assertEquals(Set.of(0, 1, 2), roundOneIndexes);
    }

    @Test
    void cancellingPartialJoinForMultipleInstancesCancelsTheLateInstance() {
        final String flowId = startPatternFlow(
                CancellingPartialJoinForMultipleInstancesFlow.class,
                "instances-cancelling-join",
                new CancellingPartialJoinForMultipleInstancesFlow.QuorumInspectionPlan(3, 2));
        final CancellingPartialJoinForMultipleInstancesFlow stub =
                rpcStub(CancellingPartialJoinForMultipleInstancesFlow.class, flowId);
        client().invokeRPC(stub::releaseQuorumInspections, 2);
        waitForStep(flowId, "AcceptQuorumInspections", 1);
        client().invokeRPC(stub::releaseQuorumInspections, 1);
        assertEquals(1, client().invokeRPC(stub::getQueuedQuorumReleaseCount));
        client().invokeRPC(stub::certifyQuorum);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("CertifyQuorum"), stepTypesOf(completions));
        final List<Integer> indexes = integersOf(completions.get(0));
        assertEquals(2, indexes.size());
        assertEquals(2, new HashSet<Integer>(indexes).size());
    }

    @Test
    void dynamicPartialJoinForMultipleInstancesClosesAdmissionAfterTwoAcceptedBids()
            throws InterruptedException {
        final String flowId = startPatternFlow(
                DynamicPartialJoinForMultipleInstancesFlow.class,
                "instances-dynamic-join",
                new DynamicPartialJoinForMultipleInstancesFlow.BidJoin(2, List.of()));
        final DynamicPartialJoinForMultipleInstancesFlow stub =
                rpcStub(DynamicPartialJoinForMultipleInstancesFlow.class, flowId);
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::addBid, 500)));
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::addBid, 1500)));
        awaitRpcValue(
                () -> client().invokeRPC(stub::getStartedEvaluationCount), 2, "started evaluations");
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::addBid, 700)));
        awaitRpcValue(
                () -> client().invokeRPC(stub::getStartedEvaluationCount), 3, "started evaluations");
        client().invokeRPC(stub::openBidGate, 3);
        waitForAttributeValue(
                flowId, DynamicPartialJoinForMultipleInstancesFlow.isBidAdmissionClosed, true);
        assertFalse(retryOnLockConflict(() -> client().invokeRPC(stub::addBid, 400)));
        client().invokeRPC(stub::signContract);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("AwardContract"), stepTypesOf(completions));
        assertEquals(List.of(500, 700), integersOf(completions.get(0)));
    }

    // ---- state based ----

    @Test
    void deferredChoiceRunsTheBranchOfTheSingleResponse() {
        final String flowId = startPatternFlow(DeferredChoiceFlow.class, "deferred-single", null);
        final DeferredChoiceFlow stub = rpcStub(DeferredChoiceFlow.class, flowId);
        client().invokeRPC(stub::presentOffer);
        waitForStep(flowId, "PresentOffer", 1);
        client().invokeRPC(stub::declineOffer);
        assertEquals(List.of("ArchiveOffer"), stepTypesOf(waitForCompletedFlow(flowId)));
    }

    @Test
    void deferredChoiceRunsExactlyOneBranchForCompetingResponses() {
        final String flowId = startPatternFlow(DeferredChoiceFlow.class, "deferred-competing", null);
        final DeferredChoiceFlow stub = rpcStub(DeferredChoiceFlow.class, flowId);
        client().invokeRPC(stub::acceptOffer);
        client().invokeRPC(stub::declineOffer);
        client().invokeRPC(stub::presentOffer);
        final List<String> stepTypes = stepTypesOf(waitForCompletedFlow(flowId));
        assertEquals(1, stepTypes.size());
        assertTrue(Set.of("FulfillOffer", "ArchiveOffer").contains(stepTypes.get(0)));
    }

    @Test
    void interleavedParallelRoutingNeverOverlapsAndKeepsThePartialOrder() {
        final String flowId =
                startPatternFlow(InterleavedParallelRoutingFlow.class, "interleaved-parallel", null);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("CompleteOnboardingTasks"), stepTypesOf(completions));
        final List<TaskInterval> intervals = intervalsOf(completions.get(0));
        assertEquals(3, intervals.size());
        assertEquals(
                Set.of("PrepareContract", "SignContract", "RunCreditCheck"), tasksOf(intervals));
        assertNoOverlap(intervals);
        final TaskInterval prepare = intervalOf(intervals, "PrepareContract");
        final TaskInterval sign = intervalOf(intervals, "SignContract");
        assertTrue(prepare.finishedAtMillis() <= sign.startedAtMillis());
    }

    @Test
    void milestoneEnablesAChangeOnlyWhileTheWindowIsOpen() throws InterruptedException {
        final String flowId = startPatternFlow(MilestoneFlow.class, "milestone", null);
        final MilestoneFlow stub = rpcStub(MilestoneFlow.class, flowId);
        waitForAttributeValue(flowId, MilestoneFlow.isChangeWindowOpen, true);
        assertTrue(client().invokeRPC(stub::requestConfigChange, "raise-limit"));
        client().invokeRPC(stub::approveRelease);
        waitForAttributeValue(flowId, MilestoneFlow.isChangeWindowOpen, false);
        assertFalse(client().invokeRPC(stub::requestConfigChange, "late-change"));
        client().invokeRPC(stub::finalizeRelease);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of("raise-limit"), outputsOf(completions, "ApplyConfigChange", String.class));
    }

    @Test
    void criticalSectionRunsTheTwoSectionsWithoutOverlap() {
        final String flowId = startPatternFlow(CriticalSectionFlow.class, "critical-section", null);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("FinishCriticalSections"), stepTypesOf(completions));
        final List<TaskInterval> intervals = intervalsOf(completions.get(0));
        assertEquals(2, intervals.size());
        assertEquals(Set.of("inventory", "pricing"), tasksOf(intervals));
        assertNoOverlap(intervals);
    }

    @Test
    void interleavedRoutingRunsAuditsOneAtATime() {
        final String flowId = startPatternFlow(InterleavedRoutingFlow.class, "interleaved", null);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("CompleteAudits"), stepTypesOf(completions));
        final List<TaskInterval> intervals = intervalsOf(completions.get(0));
        assertEquals(3, intervals.size());
        assertEquals(Set.of("AuditPayroll", "AuditExpenses", "AuditTaxes"), tasksOf(intervals));
        assertNoOverlap(intervals);
    }

    // ---- cancellation ----

    @Test
    void cancelTaskWithdrawsTheWaitingStepAndRunsTheAlternative() {
        final String flowId = startPatternFlow(CancelTaskFlow.class, "cancel-task", null);
        final CancelTaskFlow stub = rpcStub(CancelTaskFlow.class, flowId);
        client().invokeRPC(stub::cancelCourierPickup);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("ArrangeSelfDelivery"), stepTypesOf(completions));
    }

    @Test
    void cancelCaseStopsTheChildFlowAndFailsTheCase() throws InterruptedException {
        final String flowId = startPatternFlow(CancelCaseFlow.class, "cancel-case", null);
        waitForStep(flowId, "StartApplication", 1);
        final CancelCaseFlow stub = rpcStub(CancelCaseFlow.class, flowId);
        client().invokeRPC(stub::cancelCase, "applicant withdrew");
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.FAILED, result.getStatus(), result.getErrorMessage());
        assertTrue(outputsOf(result.getCompletions(), "ReviewApplication", String.class).isEmpty());
        final String[] stoppedChildFlowIds = client()
                .getAttributes(flowId, CancelCaseFlow.stoppedChildFlowIds)
                .get(CancelCaseFlow.stoppedChildFlowIds)
                .orElseThrow(() -> new AssertionError("stoppedChildFlowIds was not set"));
        assertEquals(1, stoppedChildFlowIds.length);
        environment().awaitCondition(
                () -> client().describeFlow(stoppedChildFlowIds[0]).getStatus(),
                FlowStatus.CANCELED::equals,
                WAIT_TIMEOUT,
                "the background check child never reached CANCELED");
    }

    @Test
    void cancelRegionWithdrawsTwoStepTypesAndKeepsTheRest() {
        final String flowId = startPatternFlow(CancelRegionFlow.class, "cancel-region", null);
        final CancelRegionFlow stub = rpcStub(CancelRegionFlow.class, flowId);
        waitForStep(flowId, "StartApplicationChecks", 1);
        client().invokeRPC(stub::rejectApplication);
        waitForStep(flowId, "NotifyRejection", 1);
        assertRunning(flowId);
        client().invokeRPC(stub::completeSanctionsScreening);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("NotifyRejection", "ScreenSanctions"), stepTypesOf(completions));
    }

    @Test
    void cancelMultipleInstanceTaskKeepsTheCompletedInstance() throws InterruptedException {
        final String flowId =
                startPatternFlow(CancelMultipleInstanceTaskFlow.class, "cancel-instances", 3);
        final CancelMultipleInstanceTaskFlow stub =
                rpcStub(CancelMultipleInstanceTaskFlow.class, flowId);
        waitForStep(flowId, "StartTranslations", 1);
        client().invokeRPC(stub::releaseTranslation);
        waitForAttributeValue(flowId, CancelMultipleInstanceTaskFlow.finishedTranslationCount, 1);
        client().invokeRPC(stub::cancelTranslations);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(1, outputsOf(completions, "TranslateDocument", Integer.class).size());
        assertEquals(List.of(1), outputsOf(completions, "ReportTranslations", Integer.class));
    }

    @Test
    void completeMultipleInstanceTaskContinuesWithTheSubmittedReviews() throws InterruptedException {
        final String flowId =
                startPatternFlow(CompleteMultipleInstanceTaskFlow.class, "complete-instances", 3);
        final CompleteMultipleInstanceTaskFlow stub =
                rpcStub(CompleteMultipleInstanceTaskFlow.class, flowId);
        waitForStep(flowId, "StartPeerReviews", 1);
        client().invokeRPC(stub::releasePeerReview);
        awaitRpcValue(
                () -> client().invokeRPC(stub::getSubmittedPeerReviewCount), 1, "submitted reviews");
        client().invokeRPC(stub::completePeerReviewsEarly);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("PublishReviewResults"), stepTypesOf(completions));
        assertEquals(1, integersOf(completions.get(0)).size());
    }

    // ---- iteration ----

    @Test
    void arbitraryCyclesEnterAtDraftAndExitThroughPublish() {
        assertEquals(
                List.of(
                        "RouteSubmission",
                        "DraftArticle",
                        "EditArticle",
                        "DraftArticle",
                        "EditArticle",
                        "PublishArticle"),
                runArbitraryCycles("cycles-draft", false, false, "PublishArticle"));
    }

    @Test
    void arbitraryCyclesEnterAtEditAndExitThroughPublish() {
        assertEquals(
                List.of("RouteSubmission", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle"),
                runArbitraryCycles("cycles-edit", true, false, "PublishArticle"));
    }

    @Test
    void arbitraryCyclesExitThroughAbandon() {
        assertEquals(
                List.of(
                        "RouteSubmission",
                        "DraftArticle",
                        "EditArticle",
                        "DraftArticle",
                        "AbandonArticle"),
                runArbitraryCycles("cycles-abandon", false, true, "AbandonArticle"));
    }

    @Test
    void structuredLoopRunsTheBodyOncePerRemainingBatch() {
        final String flowId = startPatternFlow(
                StructuredLoopFlow.class, "loop-three", new StructuredLoopFlow.BatchProgress(3, List.of()));
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("FinishBatches"), stepTypesOf(completions));
        assertEquals(List.of("batch-3", "batch-2", "batch-1"), stringsOf(completions.get(0)));
    }

    @Test
    void structuredLoopSkipsTheBodyWhenThePreTestFails() {
        final String flowId = startPatternFlow(
                StructuredLoopFlow.class, "loop-empty", new StructuredLoopFlow.BatchProgress(0, List.of()));
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("FinishBatches"), stepTypesOf(completions));
        assertEquals(List.of(), stringsOf(completions.get(0)));
    }

    @Test
    void factorialRecursionWaitsForSubFlowsOfItsOwnType() {
        final String flowId = startPatternFlow(FactorialRecursionFlow.class, "recursion", 4);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of(24), outputsOf(completions, "ComputeFactorial", Integer.class));
    }

    // ---- termination ----

    @Test
    void implicitTerminationClosesOnlyWhenNoStepIsActive() {
        final String flowId = startPatternFlow(ImplicitTerminationFlow.class, "implicit", null);
        waitForStep(flowId, "CreateAccount", 1);
        waitForStep(flowId, "SendWelcomeEmail", 1);
        assertRunning(flowId);
        final ImplicitTerminationFlow stub = rpcStub(ImplicitTerminationFlow.class, flowId);
        client().invokeRPC(stub::completeProfile);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of("AwaitProfileCompletion", "CreateAccount", "SendWelcomeEmail"),
                stepTypesOf(completions));
    }

    @Test
    void explicitTerminationClosesAtOnceAndCancelsTheParkedSteps() {
        final String flowId = startPatternFlow(ExplicitTerminationFlow.class, "explicit", null);
        waitForStep(flowId, "StartIncident", 1);
        final ExplicitTerminationFlow stub = rpcStub(ExplicitTerminationFlow.class, flowId);
        client().invokeRPC(stub::resolveIncident);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("ResolveIncident"), stepTypesOf(completions));
    }

    // ---- triggers ----

    @Test
    void transientTriggerAcceptsOnlyTheFirstEventWhileAStepWaits() throws InterruptedException {
        final String flowId = startPatternFlow(TransientTriggerFlow.class, "transient", null);
        final TransientTriggerFlow stub = rpcStub(TransientTriggerFlow.class, flowId);
        assertFalse(retryOnLockConflict(() -> client().invokeRPC(stub::sendMotionEvent, 1)));
        client().invokeRPC(stub::armMotionSensor);
        waitForAttributeValue(flowId, TransientTriggerFlow.isAwaitingMotionEvent, true);
        assertTrue(retryOnLockConflict(() -> client().invokeRPC(stub::sendMotionEvent, 2)));
        assertFalse(retryOnLockConflict(() -> client().invokeRPC(stub::sendMotionEvent, 3)));
        client().invokeRPC(stub::stopMotionMonitoring);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("AwaitMonitoringStop"), stepTypesOf(completions));
        assertEquals(List.of(2), outputsOf(completions, "AwaitMonitoringStop", Integer.class));
    }

    @Test
    void persistentTriggerKeepsAnEarlyEventQueued() {
        final String flowId = startPatternFlow(PersistentTriggerFlow.class, "persistent", null);
        final PersistentTriggerFlow stub = rpcStub(PersistentTriggerFlow.class, flowId);
        client().invokeRPC(stub::sendShipmentLabel, "label-1");
        client().invokeRPC(stub::markCarrierReady);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("label-1"), outputsOf(completions, "AwaitShipmentLabel", String.class));
    }

    // ---- helpers ----

    private static IntegEnvironment environment() {
        return SharedIntegExtension.environment();
    }

    private static Client client() {
        return environment().client();
    }

    private static <I, F extends Flow<I>> String startPatternFlow(
            final Class<F> flowClass, final String prefix, final I input) {
        final String flowId = environment().newFlowId("wcp-" + prefix);
        client().startFlow(
                environment().workflowPatternFlow(flowClass), flowId, input, environment().startOptions());
        return flowId;
    }

    private static <F> F rpcStub(final Class<F> flowClass, final String flowId) {
        return client().newRpcStub(flowClass, flowId);
    }

    private static List<StepCompletion> waitForCompletedFlow(final String flowId) {
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), flowId + ": " + result.getErrorMessage());
        return result.getCompletions();
    }

    private static void waitForStep(final String flowId, final String stepType, final int number) {
        client().waitForStepCompletion(
                flowId,
                StepExecutionId.of(stepType, number),
                WaitForStepCompletionOptions.newBuilder().requestTimeout(WAIT_TIMEOUT).build());
    }

    private static void assertRunning(final String flowId) {
        assertEquals(FlowStatus.RUNNING, client().describeFlow(flowId).getStatus());
    }

    private static <T> void waitForAttributeValue(
            final String flowId, final Attribute<T> attribute, final T expected)
            throws InterruptedException {
        environment().awaitCondition(
                () -> client().getAttributes(flowId, attribute).get(attribute).orElse(null),
                expected::equals,
                WAIT_TIMEOUT,
                flowId + " Attribute never became " + expected);
    }

    private static <T> T attributeValue(final String flowId, final Attribute<T> attribute) {
        return client().getAttributes(flowId, attribute).get(attribute).orElse(null);
    }

    private static void awaitRpcValue(
            final Supplier<Integer> read, final int expected, final String description)
            throws InterruptedException {
        environment().awaitCondition(
                read, value -> value == expected, WAIT_TIMEOUT, description + " never reached " + expected);
    }

    // The RPC fails fast while a Step holds the same Attribute lock.
    private static <T> T retryOnLockConflict(final Supplier<T> invoke) {
        final long deadline = System.nanoTime() + WAIT_TIMEOUT.toNanos();
        while (true) {
            try {
                return invoke.get();
            } catch (final RpcLockConflictException conflict) {
                if (System.nanoTime() >= deadline) {
                    throw conflict;
                }
                sleepBeforeRetry();
            }
        }
    }

    private static void sleepBeforeRetry() {
        try {
            Thread.sleep(LOCK_CONFLICT_RETRY_MILLIS);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted retrying an RPC", interrupted);
        }
    }

    private static void releaseSupplierQuotes(final String flowId, final String... suppliers) {
        final StructuredPartialJoinFlow stub = rpcStub(StructuredPartialJoinFlow.class, flowId);
        for (final String supplier : suppliers) {
            client().invokeRPC(stub::releaseSupplierQuote, supplier);
        }
    }

    private static void sendBranchSignals(final String flowId, final String... branches) {
        final BlockingPartialJoinFlow stub = rpcStub(BlockingPartialJoinFlow.class, flowId);
        for (final String branch : branches) {
            client().invokeRPC(stub::sendBranchSignal, branch);
        }
    }

    private static List<StepCompletion> runGeneralSynchronizingMerge(
            final String prefix, final GeneralSynchronizingMergeFlow.DocumentReviewPlan plan) {
        return waitForCompletedFlow(
                startPatternFlow(GeneralSynchronizingMergeFlow.class, prefix, plan));
    }

    private static List<String> runArbitraryCycles(
            final String prefix,
            final boolean hasDraft,
            final boolean shouldAbandon,
            final String exitStepType) {
        final String flowId = startPatternFlow(
                ArbitraryCyclesFlow.class,
                prefix,
                new ArbitraryCyclesFlow.ArticleSubmission(hasDraft, shouldAbandon));
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of(exitStepType), stepTypesOf(completions));
        return stringsOf(completions.get(0));
    }

    private static List<String> stepTypesOf(final List<StepCompletion> completions) {
        return completions.stream().map(StepCompletion::getStepType).sorted().toList();
    }

    private static <T> List<T> outputsOf(
            final List<StepCompletion> completions, final String stepType, final Class<T> outputType) {
        return completions.stream()
                .filter(completion -> completion.getStepType().equals(stepType))
                .map(completion -> completion.getOutput(outputType))
                .toList();
    }

    private static <T> List<T> outputsInExecutionOrder(
            final List<StepCompletion> completions, final String stepType, final Class<T> outputType) {
        return completions.stream()
                .filter(completion -> completion.getStepType().equals(stepType))
                .sorted(Comparator.comparingInt(WorkflowPatternsIntegTest::executionNumberOf))
                .map(completion -> completion.getOutput(outputType))
                .toList();
    }

    private static int executionNumberOf(final StepCompletion completion) {
        final String executionId = completion.getStepExecutionId();
        final String prefix = completion.getStepType() + "-";
        if (!executionId.startsWith(prefix)) {
            throw new AssertionError("unexpected Step execution ID: " + executionId);
        }
        return Integer.parseInt(executionId.substring(prefix.length()));
    }

    private static List<String> stringsOf(final StepCompletion completion) {
        return List.of(completion.getOutput(String[].class));
    }

    private static List<Integer> integersOf(final StepCompletion completion) {
        return List.of(completion.getOutput(Integer[].class));
    }

    private static List<TaskInterval> intervalsOf(final StepCompletion completion) {
        return List.of(completion.getOutput(TaskInterval[].class));
    }

    private static Set<String> tasksOf(final List<TaskInterval> intervals) {
        final Set<String> tasks = new HashSet<String>();
        for (final TaskInterval interval : intervals) {
            tasks.add(interval.task());
        }
        return tasks;
    }

    private static TaskInterval intervalOf(final List<TaskInterval> intervals, final String task) {
        return intervals.stream()
                .filter(interval -> interval.task().equals(task))
                .findFirst()
                .orElseThrow(() -> new AssertionError("no interval for " + task));
    }

    private static void assertNoOverlap(final List<TaskInterval> intervals) {
        final List<TaskInterval> ordered = new ArrayList<TaskInterval>(intervals);
        ordered.sort(Comparator.comparingLong(TaskInterval::startedAtMillis));
        for (int index = 1; index < ordered.size(); index++) {
            final TaskInterval previous = ordered.get(index - 1);
            final TaskInterval current = ordered.get(index);
            assertTrue(
                    previous.finishedAtMillis() <= current.startedAtMillis(),
                    previous.task() + " overlaps " + current.task());
        }
    }
}
