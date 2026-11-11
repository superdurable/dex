/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PackagePrivateTestAccess;
import io.superdurable.dex.RPCInvokeOptions;
import io.superdurable.dex.Registry;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.FlowDefinitionException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class JoinIntegTest {
    private static final Duration STEP_TIMEOUT = Duration.ofSeconds(20);
    private static final Duration FLOW_TIMEOUT = Duration.ofSeconds(30);

    @TempDir
    Path cacheDirectory;

    @Test
    void testAllBranchesJoinCountsResultsReportedBeforeTheJoinWaits() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        final List<String> names = Arrays.asList("A", "B", "C");
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = startAllOf(
                    environment,
                    workflow,
                    "join-all-of",
                    names,
                    JoinIntegAllOfWorkflow.JoinMode.LATE_JOIN_START);
            final JoinIntegAllOfWorkflow stub = stub(environment, flowId);
            for (String name : Arrays.asList("A", "C", "B")) {
                environment.client().invokeRPC(stub::releaseBranch, name);
                // Branch execution numbers follow fork order.
                waitForStep(environment, flowId, "JoinIntegAllOfBranch", names.indexOf(name) + 1);
            }
            // The join Step starts only now, after every branch result committed.
            environment.client().invokeRPC(stub::releaseJoinGate);
            final JoinIntegAllOfWorkflow.JoinOutcome outcome = allOfOutcome(environment, flowId);
            assertEquals(JoinIntegAllOfWorkflow.SPLIT_STEP_TYPE + "-1", outcome.roundKey);
            assertEquals(3, outcome.branchCount);
            assertEquals(Arrays.asList("A", "C", "B"), outcome.results);
            assertEquals(Collections.emptyList(), outcome.remainingResults);
        }
    }

    @Test
    void testRunTimeBranchCountEqualsTheInputListLength() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        final List<String> reviewers = Arrays.asList("R1", "R2", "R3", "R4", "R5");
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = startAllOf(
                    environment,
                    workflow,
                    "join-multi-instance",
                    reviewers,
                    JoinIntegAllOfWorkflow.JoinMode.ALL_BRANCHES);
            final JoinIntegAllOfWorkflow stub = stub(environment, flowId);
            for (String reviewer : reviewers) {
                environment.client().invokeRPC(stub::releaseBranch, reviewer);
            }
            final JoinIntegAllOfWorkflow.JoinOutcome outcome = allOfOutcome(environment, flowId);
            assertEquals(reviewers.size(), outcome.branchCount);
            final List<String> sortedResults = new ArrayList<String>(outcome.results);
            Collections.sort(sortedResults);
            assertEquals(reviewers, sortedResults);
        }
    }

    @Test
    void testPartialJoinResetBlocksTheNextRoundUntilEveryBranchReported() throws Exception {
        final JoinIntegPartialWorkflow workflow = new JoinIntegPartialWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = "join-partial-" + UUID.randomUUID();
            environment.client().startFlow(
                    workflow,
                    flowId,
                    new JoinIntegPartialWorkflow.QuoteRequest());
            final JoinIntegPartialWorkflow stub = environment.client().newRpcStub(
                    JoinIntegPartialWorkflow.class,
                    flowId);
            environment.client().invokeRPC(
                    stub::releaseSupplier,
                    JoinIntegPartialWorkflow.releaseInstance("A", 1));
            environment.client().invokeRPC(
                    stub::releaseSupplier,
                    JoinIntegPartialWorkflow.releaseInstance("B", 1));
            waitForStep(environment, flowId, JoinIntegPartialWorkflow.ACCEPT_STEP_TYPE, 1);

            final String earlyRoundTwoRelease = JoinIntegPartialWorkflow.releaseInstance("A", 2);
            environment.client().invokeRPC(stub::releaseSupplier, earlyRoundTwoRelease);
            // Round 2 has not forked: its enablement stays queued while round 1 is unreset.
            assertEquals(
                    Integer.valueOf(1),
                    environment.client().invokeRPC(
                            stub::getSupplierReleaseSize,
                            earlyRoundTwoRelease));

            environment.client().invokeRPC(
                    stub::releaseSupplier,
                    JoinIntegPartialWorkflow.releaseInstance("C", 1));
            environment.client().invokeRPC(
                    stub::releaseSupplier,
                    JoinIntegPartialWorkflow.releaseInstance("B", 2));
            final FlowResult result = environment.client().waitForFlow(flowId, FLOW_TIMEOUT);
            assertEquals(FlowStatus.COMPLETED, result.getStatus());
            final JoinIntegPartialWorkflow.QuoteOutcome outcome =
                    result.getSingleOutput(JoinIntegPartialWorkflow.QuoteOutcome.class);
            assertEquals(
                    Arrays.asList("JoinIntegPartialRequest-1", "JoinIntegPartialRequest-2"),
                    outcome.roundKeys);
            // Released branches run concurrently, so their commit order within a round varies.
            for (List<String> acceptedQuotes : outcome.acceptedQuotes) {
                Collections.sort(acceptedQuotes);
            }
            assertEquals(
                    Arrays.asList(Arrays.asList("A", "B"), Arrays.asList("A", "B")),
                    outcome.acceptedQuotes);
            assertEquals(Collections.singletonList(Collections.singletonList("C")),
                    outcome.remainingQuotes);
        }
    }

    @Test
    void testResultsReportedToAnotherRoundKeyDoNotSatisfyTheRound() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = startAllOf(
                    environment,
                    workflow,
                    "join-key-isolation",
                    Arrays.asList("A", "B"),
                    JoinIntegAllOfWorkflow.JoinMode.ALL_BRANCHES);
            final JoinIntegAllOfWorkflow stub = stub(environment, flowId);
            final JoinRound otherRound = new JoinRound("OtherRound-1", 2);
            environment.client().invokeRPC(
                    stub::reportResultToRound,
                    new JoinIntegAllOfWorkflow.RoundReport(otherRound, "other-1"));
            environment.client().invokeRPC(
                    stub::reportResultToRound,
                    new JoinIntegAllOfWorkflow.RoundReport(otherRound, "other-2"));
            environment.client().invokeRPC(stub::releaseBranch, "A");
            environment.client().invokeRPC(stub::releaseBranch, "B");
            final JoinIntegAllOfWorkflow.JoinOutcome outcome = allOfOutcome(environment, flowId);
            final List<String> sortedResults = new ArrayList<String>(outcome.results);
            Collections.sort(sortedResults);
            assertEquals(Arrays.asList("A", "B"), sortedResults);
        }
    }

    @Test
    void testFirstBranchJoinCancelsSiblingsAndKeepsTheLateResult() throws Exception {
        final JoinIntegCancellingWorkflow workflow = new JoinIntegCancellingWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = "join-cancelling-" + UUID.randomUUID();
            environment.client().startFlow(
                    workflow,
                    flowId,
                    new JoinIntegCancellingWorkflow.CarrierList(Arrays.asList("A", "B", "C")));
            final JoinIntegCancellingWorkflow stub = environment.client().newRpcStub(
                    JoinIntegCancellingWorkflow.class,
                    flowId);
            environment.client().invokeRPC(stub::releaseCarrier, "A");
            waitForStep(environment, flowId, JoinIntegCancellingWorkflow.BRANCH_STEP_TYPE, 1);
            environment.client().invokeRPC(stub::releaseCarrier, "B");
            waitForStep(environment, flowId, JoinIntegCancellingWorkflow.BRANCH_STEP_TYPE, 2);
            environment.client().invokeRPC(stub::releaseJoinGate);
            waitForStep(environment, flowId, JoinIntegCancellingWorkflow.JOIN_STEP_TYPE, 1);
            // Branch C is canceled, so releasing it now cannot produce a result.
            environment.client().invokeRPC(stub::releaseCarrier, "C");

            final String roundKey = "JoinIntegCancellingSplit-1";
            final JoinIntegCancellingWorkflow.PendingBids pendingBids = environment.client().invokeRPC(
                    stub::getPendingBids,
                    roundKey,
                    RPCInvokeOptions.newBuilder()
                            .addLoadChannelMapInstance(
                                    PackagePrivateTestAccess.channelMapOf(workflow.bidReceived),
                                    roundKey)
                            .build());
            assertEquals(1, pendingBids.size);
            assertEquals(Collections.singletonList("B"), pendingBids.values);

            environment.client().invokeRPC(stub::finishRun);
            final FlowResult result = environment.client().waitForFlow(flowId, FLOW_TIMEOUT);
            assertEquals(FlowStatus.COMPLETED, result.getStatus());
            final JoinIntegCancellingWorkflow.BidOutcome outcome =
                    result.getSingleOutput(JoinIntegCancellingWorkflow.BidOutcome.class);
            assertEquals(Collections.singletonList("A"), outcome.winningBids);
        }
    }

    @Test
    void testRpcReportsForACanceledBranchAndTheAllBranchesJoinFires() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = startAllOf(
                    environment,
                    workflow,
                    "join-report-canceled",
                    Arrays.asList("A", "B"),
                    JoinIntegAllOfWorkflow.JoinMode.ALL_BRANCHES);
            final JoinIntegAllOfWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::releaseBranch, "A");
            waitForStep(environment, flowId, "JoinIntegAllOfBranch", 1);
            final JoinRound round = new JoinRound(JoinIntegAllOfWorkflow.SPLIT_STEP_TYPE + "-1", 2);
            environment.client().invokeRPC(
                    stub::cancelBranchesAndReport,
                    new JoinIntegAllOfWorkflow.RoundReport(round, "B-canceled"));
            final JoinIntegAllOfWorkflow.JoinOutcome outcome = allOfOutcome(environment, flowId);
            assertEquals(Arrays.asList("A", "B-canceled"), outcome.results);
        }
    }

    @Test
    void testForkingTheSameJoinTwiceInOneInvocationFailsTheStep() throws Exception {
        final FlowResult result = runMisuse(JoinIntegInvalidUsageWorkflow.Misuse.FORK_TWICE);
        assertEquals(FlowStatus.FAILED, result.getStatus());
        assertTrue(
                result.getErrorMessage().contains("was already forked in this invocation"),
                result.getErrorMessage());
    }

    @Test
    void testRpcForkRulesForRoundKeys() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = startAllOf(
                    environment,
                    workflow,
                    "join-rpc-fork",
                    Collections.singletonList("A"),
                    JoinIntegAllOfWorkflow.JoinMode.LATE_JOIN_START);
            final JoinIntegAllOfWorkflow stub = stub(environment, flowId);

            final WorkerInvocationException forkFailure = assertThrows(
                    WorkerInvocationException.class,
                    () -> environment.client().invokeRPC(
                            stub::forkWithoutStepExecution,
                            new JoinIntegAllOfWorkflow.SplitInput(
                                    Collections.singletonList("X"),
                                    JoinIntegAllOfWorkflow.JoinMode.ALL_BRANCHES)));
            assertTrue(
                    forkFailure.getMessage().contains("use forkWithRoundKey in an RPC"),
                    forkFailure.getMessage());
            final WorkerInvocationException keyFailure = assertThrows(
                    WorkerInvocationException.class,
                    () -> environment.client().invokeRPC(
                            stub::forkFromRpc,
                            new JoinIntegAllOfWorkflow.RpcFork(
                                    "bad/key",
                                    Collections.singletonList("X"))));
            assertTrue(
                    keyFailure.getMessage().contains("must not contain '/'"),
                    keyFailure.getMessage());

            environment.client().invokeRPC(
                    stub::forkFromRpc,
                    new JoinIntegAllOfWorkflow.RpcFork("rpc-round", Collections.singletonList("X")));
            environment.client().invokeRPC(stub::releaseBranch, "X");
            environment.client().invokeRPC(stub::releaseBranch, "A");
            environment.client().invokeRPC(stub::releaseJoinGate);
            final FlowResult result = environment.client().waitForFlow(flowId, FLOW_TIMEOUT);
            assertEquals(FlowStatus.COMPLETED, result.getStatus());
            final List<String> roundSummaries = new ArrayList<String>();
            for (StepCompletion completion : result.getCompletions()) {
                final JoinIntegAllOfWorkflow.JoinOutcome outcome =
                        completion.getOutput(JoinIntegAllOfWorkflow.JoinOutcome.class);
                roundSummaries.add(outcome.roundKey + ":" + outcome.results);
            }
            Collections.sort(roundSummaries);
            assertEquals(
                    Arrays.asList(JoinIntegAllOfWorkflow.SPLIT_STEP_TYPE + "-1:[A]", "rpc-round:[X]"),
                    roundSummaries);
        }
    }

    @Test
    void testJoinConditionsOutsideTheRoundFailWaitFor() throws Exception {
        final JoinIntegInvalidUsageWorkflow.Misuse[] misuses = {
            JoinIntegInvalidUsageWorkflow.Misuse.FOR_BRANCHES_ZERO,
            JoinIntegInvalidUsageWorkflow.Misuse.FOR_BRANCHES_ABOVE_BRANCH_COUNT,
            JoinIntegInvalidUsageWorkflow.Misuse.FOR_FIRST_BRANCH_WITHOUT_BRANCHES,
            JoinIntegInvalidUsageWorkflow.Misuse.FOR_REMAINING_BRANCHES_NEGATIVE,
            JoinIntegInvalidUsageWorkflow.Misuse.FOR_REMAINING_BRANCHES_ABOVE_BRANCH_COUNT,
        };
        final JoinIntegInvalidUsageWorkflow workflow = new JoinIntegInvalidUsageWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            for (JoinIntegInvalidUsageWorkflow.Misuse misuse : misuses) {
                final String flowId = "join-invalid-condition-" + UUID.randomUUID();
                environment.client().startFlow(workflow, flowId, misuse);
                final FlowResult result = environment.client().waitForFlow(flowId, FLOW_TIMEOUT);
                assertEquals(FlowStatus.FAILED, result.getStatus(), misuse.name());
                assertTrue(
                        result.getErrorMessage().contains("Join misusedResult"),
                        misuse + ": " + result.getErrorMessage());
            }
        }
    }

    @Test
    void testZeroRemainingAndZeroBranchRoundsPassImmediately() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String emptyFlowId = startAllOf(
                    environment,
                    workflow,
                    "join-zero-branches",
                    Collections.<String>emptyList(),
                    JoinIntegAllOfWorkflow.JoinMode.ALL_BRANCHES);
            final JoinIntegAllOfWorkflow.JoinOutcome emptyOutcome =
                    allOfOutcome(environment, emptyFlowId);
            assertEquals(0, emptyOutcome.branchCount);
            assertEquals(Collections.emptyList(), emptyOutcome.results);
            assertEquals(Collections.emptyList(), emptyOutcome.remainingResults);

            // The Reset Step waits with forRemainingBranches(round, branchCount).
            final String fullFlowId = startAllOf(
                    environment,
                    workflow,
                    "join-zero-remaining",
                    Collections.singletonList("A"),
                    JoinIntegAllOfWorkflow.JoinMode.ALL_BRANCHES);
            environment.client().invokeRPC(stub(environment, fullFlowId)::releaseBranch, "A");
            final JoinIntegAllOfWorkflow.JoinOutcome fullOutcome =
                    allOfOutcome(environment, fullFlowId);
            assertEquals(Collections.singletonList("A"), fullOutcome.results);
            assertEquals(Collections.emptyList(), fullOutcome.remainingResults);
        }
    }

    @Test
    void testJoinAndChannelMapWithTheSameNameFailRegistration() {
        final FlowDefinitionException failure = assertThrows(
                FlowDefinitionException.class,
                () -> new Registry(Collections.<Flow<?>>singletonList(
                        new JoinIntegDuplicateNameWorkflow())));
        assertTrue(
                failure.getMessage().contains(JoinIntegDuplicateNameWorkflow.DUPLICATED_NAME),
                failure.getMessage());
    }

    @Test
    void testTimerDeadlineWinsWhenABranchNeverReports() throws Exception {
        final JoinIntegAllOfWorkflow workflow = new JoinIntegAllOfWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = startAllOf(
                    environment,
                    workflow,
                    "join-deadline",
                    Arrays.asList("A", "B"),
                    JoinIntegAllOfWorkflow.JoinMode.DEADLINE);
            environment.client().invokeRPC(stub(environment, flowId)::releaseBranch, "A");
            final JoinIntegAllOfWorkflow.JoinOutcome outcome = allOfOutcome(environment, flowId);
            assertEquals(JoinIntegAllOfWorkflow.TIMER_WON, outcome.winner);
            assertEquals(Collections.emptyList(), outcome.results);
        }
    }

    private DexDevTestEnvironment start(final Flow<?> workflow) throws Exception {
        return DexDevTestEnvironment.start(cacheDirectory, workflow);
    }

    private static String startAllOf(
            final DexDevTestEnvironment environment,
            final JoinIntegAllOfWorkflow workflow,
            final String prefix,
            final List<String> names,
            final JoinIntegAllOfWorkflow.JoinMode joinMode) {
        final String flowId = prefix + "-" + UUID.randomUUID();
        environment.client().startFlow(
                workflow,
                flowId,
                new JoinIntegAllOfWorkflow.SplitInput(names, joinMode));
        return flowId;
    }

    private static JoinIntegAllOfWorkflow stub(
            final DexDevTestEnvironment environment,
            final String flowId) {
        return environment.client().newRpcStub(JoinIntegAllOfWorkflow.class, flowId);
    }

    private static JoinIntegAllOfWorkflow.JoinOutcome allOfOutcome(
            final DexDevTestEnvironment environment,
            final String flowId) {
        final FlowResult result = environment.client().waitForFlow(flowId, FLOW_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        return result.getSingleOutput(JoinIntegAllOfWorkflow.JoinOutcome.class);
    }

    private FlowResult runMisuse(final JoinIntegInvalidUsageWorkflow.Misuse misuse)
            throws Exception {
        final JoinIntegInvalidUsageWorkflow workflow = new JoinIntegInvalidUsageWorkflow();
        try (DexDevTestEnvironment environment = start(workflow)) {
            final String flowId = "join-misuse-" + UUID.randomUUID();
            environment.client().startFlow(workflow, flowId, misuse);
            return environment.client().waitForFlow(flowId, FLOW_TIMEOUT);
        }
    }

    private static void waitForStep(
            final DexDevTestEnvironment environment,
            final String flowId,
            final String stepType,
            final int executionNumber) {
        environment.client().waitForStepCompletion(
                flowId,
                StepExecutionId.of(stepType, executionNumber),
                WaitForStepCompletionOptions.newBuilder()
                        .requestId(flowId + "-" + stepType + "-" + executionNumber)
                        .requestTimeout(STEP_TIMEOUT)
                        .build());
    }
}
