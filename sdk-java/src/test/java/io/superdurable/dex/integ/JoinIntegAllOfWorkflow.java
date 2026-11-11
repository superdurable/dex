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

import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;

/** Forks one branch per name and joins them all; the join may start late or have a deadline. */
class JoinIntegAllOfWorkflow implements Flow<JoinIntegAllOfWorkflow.SplitInput> {
    static final String SPLIT_STEP_TYPE = "JoinIntegAllOfSplit";
    static final String JOIN_STEP_TYPE = "JoinIntegAllOfJoin";
    static final String TIMER_WON = "timer";
    static final String BRANCHES_WON = "branches";
    static final String JOIN_CANCELED = "join canceled";

    enum JoinMode {
        ALL_BRANCHES,
        LATE_JOIN_START,
        DEADLINE
    }

    final Join<String> branchResults = Join.define("branchResult", String.class);
    final ChannelMap<String> branchReleased = ChannelMap.define("branchReleased", String.class);
    final Channel<String> joinGate = Channel.define("joinGate", String.class);
    private final JoinIntegAllOfSplit split = new JoinIntegAllOfSplit();
    private final JoinIntegAllOfBranch branch = new JoinIntegAllOfBranch();
    private final JoinIntegAllOfGate gate = new JoinIntegAllOfGate();
    private final JoinIntegAllOfJoin join = new JoinIntegAllOfJoin();
    private final JoinIntegAllOfReset reset = new JoinIntegAllOfReset();
    private final JoinIntegAllOfHandoffBranch handoffBranch = new JoinIntegAllOfHandoffBranch();
    private final JoinIntegAllOfCanceledJoin canceledJoin = new JoinIntegAllOfCanceledJoin();

    @Override
    public StepList<SplitInput> getSteps() {
        return StepList.startStep(split).otherSteps(branch, gate, join, reset, handoffBranch, canceledJoin);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(branchResults, branchReleased, joinGate);
    }

    @RPC
    public void releaseBranch(final Context context, final String name) {
        branchReleased.publish(context, name, name);
    }

    @RPC
    public void releaseJoinGate(final Context context) {
        joinGate.publish(context, "open");
    }

    @RPC
    public void reportResultToRound(final Context context, final RoundReport report) {
        branchResults.reportBranchResult(context, report.round, report.result);
    }

    @RPC
    public RPCResult<Void> cancelJoinWithBranches(final Context context) {
        return RPCResult.<Void>of(null, StepMovement.of(JoinIntegAllOfCanceledJoin.class, JOIN_CANCELED))
                .withCancelingSteps(JoinIntegAllOfJoin.class, JoinIntegAllOfBranch.class);
    }

    @RPC
    public RPCResult<PendingCounts> getPendingCounts(final Context context, final PendingQuery query) {
        final PendingCounts counts = new PendingCounts();
        counts.results = branchResults.getChannelMap().size(context, query.roundKey);
        counts.releases = branchReleased.size(context, query.branchName);
        return RPCResult.of(counts);
    }

    @RPC
    public void forkWithoutStepExecution(final Context context, final SplitInput input) {
        branchResults.fork(context, branchBuilders(input.names), joinBuilder(input.joinMode));
    }

    @RPC
    public RPCResult<Void> forkFromRpc(final Context context, final RpcFork rpcFork) {
        final List<Function<JoinRound, StepMovement<?>>> builders = branchBuilders(rpcFork.names);
        // The handoff branch ends without reporting; a later RPC call reports its result.
        builders.add(round -> StepMovement.of(
                JoinIntegAllOfHandoffBranch.class,
                new BranchInput("handoff", round)));
        final List<StepMovement<?>> movements = branchResults.forkWithRoundKey(
                rpcFork.roundKey,
                builders,
                joinBuilder(JoinMode.ALL_BRANCHES));
        return RPCResult.of(null, movements);
    }

    private static List<Function<JoinRound, StepMovement<?>>> branchBuilders(
            final List<String> names) {
        final List<Function<JoinRound, StepMovement<?>>> builders =
                new ArrayList<Function<JoinRound, StepMovement<?>>>();
        for (String name : names) {
            builders.add(round -> StepMovement.of(
                    JoinIntegAllOfBranch.class,
                    new BranchInput(name, round)));
        }
        return builders;
    }

    private static Function<JoinRound, StepMovement<?>> joinBuilder(final JoinMode joinMode) {
        if (joinMode == JoinMode.LATE_JOIN_START) {
            return round -> StepMovement.of(JoinIntegAllOfGate.class, new JoinInput(round, joinMode));
        }
        return round -> StepMovement.of(JoinIntegAllOfJoin.class, new JoinInput(round, joinMode));
    }

    final class JoinIntegAllOfSplit implements Step<SplitInput> {
        @Override
        public Class<SplitInput> getInputType() {
            return SplitInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final SplitInput input) {
            final List<StepMovement<?>> movements = branchResults.fork(
                    context,
                    branchBuilders(input.names),
                    joinBuilder(input.joinMode));
            return StepDecision.goToMany(movements);
        }
    }

    final class JoinIntegAllOfBranch implements Step<BranchInput> {
        @Override
        public Class<BranchInput> getInputType() {
            return BranchInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final BranchInput input) {
            return Wait.until(branchReleased.forOne(input.name));
        }

        @Override
        public StepDecision execute(final Context context, final BranchInput input) {
            return branchResults.endBranch(context, input.round, input.name);
        }
    }

    final class JoinIntegAllOfHandoffBranch implements Step<BranchInput> {
        @Override
        public Class<BranchInput> getInputType() {
            return BranchInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final BranchInput input) {
            return StepDecision.deadEnd();
        }
    }

    // Runs after an RPC canceled the join Step and its branches.
    final class JoinIntegAllOfCanceledJoin implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            return Wait.until(joinGate.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final JoinOutcome outcome = new JoinOutcome();
            outcome.winner = input;
            outcome.results = new ArrayList<String>();
            return StepDecision.gracefulComplete(outcome);
        }
    }

    // Delays the join Step itself, so it starts only after the gate opens.
    final class JoinIntegAllOfGate implements Step<JoinInput> {
        @Override
        public Class<JoinInput> getInputType() {
            return JoinInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinInput input) {
            return Wait.until(joinGate.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinInput input) {
            return StepDecision.goTo(JoinIntegAllOfJoin.class, input);
        }
    }

    final class JoinIntegAllOfJoin implements Step<JoinInput> {
        @Override
        public Class<JoinInput> getInputType() {
            return JoinInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinInput input) {
            if (input.joinMode == JoinMode.DEADLINE) {
                return Wait.anyOf(
                        Timer.byDuration(Duration.ofSeconds(2), "deadline"),
                        branchResults.forAllBranches(input.round, "allBranches"));
            }
            return Wait.until(branchResults.forAllBranches(input.round));
        }

        @Override
        public StepDecision execute(final Context context, final JoinInput input) {
            final JoinOutcome outcome = new JoinOutcome();
            outcome.roundKey = input.round.getRoundKey();
            outcome.branchCount = input.round.getBranchCount();
            outcome.winner = context.hasTimerFired() ? TIMER_WON : BRANCHES_WON;
            outcome.results = new ArrayList<String>(
                    branchResults.getBranchResults(context, input.round));
            if (input.joinMode == JoinMode.DEADLINE) {
                return StepDecision.forceComplete(outcome);
            }
            return StepDecision.goTo(JoinIntegAllOfReset.class, new ResetInput(input.round, outcome));
        }
    }

    final class JoinIntegAllOfReset implements Step<ResetInput> {
        @Override
        public Class<ResetInput> getInputType() {
            return ResetInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final ResetInput input) {
            return Wait.until(branchResults.forRemainingBranches(
                    input.round,
                    input.round.getBranchCount()));
        }

        @Override
        public StepDecision execute(final Context context, final ResetInput input) {
            input.outcome.remainingResults = new ArrayList<String>(
                    branchResults.getRemainingBranchResults(context, input.round));
            return StepDecision.gracefulComplete(input.outcome);
        }
    }

    public static final class SplitInput {
        public List<String> names;
        public JoinMode joinMode;

        public SplitInput() {
        }

        SplitInput(final List<String> names, final JoinMode joinMode) {
            this.names = names;
            this.joinMode = joinMode;
        }
    }

    public static final class BranchInput {
        public String name;
        public JoinRound round;

        public BranchInput() {
        }

        BranchInput(final String name, final JoinRound round) {
            this.name = name;
            this.round = round;
        }
    }

    public static final class JoinInput {
        public JoinRound round;
        public JoinMode joinMode;

        public JoinInput() {
        }

        JoinInput(final JoinRound round, final JoinMode joinMode) {
            this.round = round;
            this.joinMode = joinMode;
        }
    }

    public static final class ResetInput {
        public JoinRound round;
        public JoinOutcome outcome;

        public ResetInput() {
        }

        ResetInput(final JoinRound round, final JoinOutcome outcome) {
            this.round = round;
            this.outcome = outcome;
        }
    }

    public static final class JoinOutcome {
        public String roundKey;
        public int branchCount;
        public String winner;
        public List<String> results;
        public List<String> remainingResults;
    }

    public static final class RoundReport {
        public JoinRound round;
        public String result;

        public RoundReport() {
        }

        RoundReport(final JoinRound round, final String result) {
            this.round = round;
            this.result = result;
        }
    }

    public static final class PendingQuery {
        public String roundKey;
        public String branchName;

        public PendingQuery() {
        }

        PendingQuery(final String roundKey, final String branchName) {
            this.roundKey = roundKey;
            this.branchName = branchName;
        }
    }

    public static final class PendingCounts {
        public int results;
        public int releases;
    }

    public static final class RpcFork {
        public String roundKey;
        public List<String> names;

        public RpcFork() {
        }

        RpcFork(final String roundKey, final List<String> names) {
            this.roundKey = roundKey;
            this.names = names;
        }
    }
}
