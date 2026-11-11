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

package io.superdurable.dex.patterns.workflowpatterns;

import io.superdurable.dex.Channel;
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
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

/** WCP-28 and WCP-31: round k + 1 does not start until every round k branch has completed. */
@Component
public class BlockingPartialJoinFlow implements Flow<BlockingPartialJoinFlow.BlockingJoinRound> {
    public static final Channel<String> branchASignal = Channel.define("branchASignal", String.class);
    public static final Channel<String> branchBSignal = Channel.define("branchBSignal", String.class);
    public static final Channel<String> branchCSignal = Channel.define("branchCSignal", String.class);
    public static final Join<String> branchCompleted = Join.define("branchCompleted", String.class);
    private static final int LAST_ROUND = 2;

    /**
     * One join round: the signals it needs, its number, and the previous Join round, which is
     * null in the first round.
     */
    public record BlockingJoinRound(int requiredSignals, int round, JoinRound previousRound) {
    }

    /** The join Step input: the blocking round and the Join round it waits on. */
    public record BlockingRoundJoin(BlockingJoinRound blockingRound, JoinRound joinRound) {
    }

    /** The branches whose signals fired the join in one round. */
    public record ProcessedRound(int round, List<String> branches) {
    }

    private final StartBlockingRound startBlockingRound = new StartBlockingRound();
    private final SignalFromBranchA signalFromBranchA = new SignalFromBranchA();
    private final SignalFromBranchB signalFromBranchB = new SignalFromBranchB();
    private final SignalFromBranchC signalFromBranchC = new SignalFromBranchC();
    private final BlockingJoin blockingJoin = new BlockingJoin();
    private final ProcessRound processRound = new ProcessRound();

    @Override
    public StepList<BlockingJoinRound> getSteps() {
        return StepList.startStep(startBlockingRound).otherSteps(
                signalFromBranchA,
                signalFromBranchB,
                signalFromBranchC,
                blockingJoin,
                processRound);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(branchASignal, branchBSignal, branchCSignal, branchCompleted);
    }

    /** Sends the signal of branch "A", "B", or "C". */
    @RPC
    public void sendBranchSignal(final Context context, final String branch) {
        branchSignalChannel(branch).publish(context, branch);
    }

    /** Counts queued branch signals, which proves a next-round signal had no effect. */
    @RPC(loadChannels = {"branchASignal", "branchBSignal", "branchCSignal"})
    public RPCResult<Integer> getQueuedBranchSignalCount(final Context context) {
        return RPCResult.of(branchASignal.size(context)
                + branchBSignal.size(context)
                + branchCSignal.size(context));
    }

    private static Channel<String> branchSignalChannel(final String branch) {
        if ("A".equals(branch)) {
            return branchASignal;
        }
        if ("B".equals(branch)) {
            return branchBSignal;
        }
        if ("C".equals(branch)) {
            return branchCSignal;
        }
        throw new IllegalArgumentException("unknown branch: " + branch);
    }

    static final class StartBlockingRound implements Step<BlockingJoinRound> {
        @Override
        public Class<BlockingJoinRound> getInputType() {
            return BlockingJoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final BlockingJoinRound blockingRound) {
            if (blockingRound.previousRound() == null) {
                return Wait.skipImmediately();
            }
            return Wait.until(branchCompleted.forRemainingBranches(
                    blockingRound.previousRound(), blockingRound.requiredSignals()));
        }

        @Override
        public StepDecision execute(final Context context, final BlockingJoinRound blockingRound) {
            return StepDecision.goToMany(branchCompleted.fork(
                    context,
                    List.of(
                            round -> StepMovement.of(SignalFromBranchA.class, round),
                            round -> StepMovement.of(SignalFromBranchB.class, round),
                            round -> StepMovement.of(SignalFromBranchC.class, round)),
                    round -> StepMovement.of(
                            BlockingJoin.class, new BlockingRoundJoin(blockingRound, round))));
        }
    }

    static final class SignalFromBranchA implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(branchASignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return branchCompleted.endBranch(context, round, "A");
        }
    }

    static final class SignalFromBranchB implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(branchBSignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return branchCompleted.endBranch(context, round, "B");
        }
    }

    static final class SignalFromBranchC implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(branchCSignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return branchCompleted.endBranch(context, round, "C");
        }
    }

    static final class BlockingJoin implements Step<BlockingRoundJoin> {
        @Override
        public Class<BlockingRoundJoin> getInputType() {
            return BlockingRoundJoin.class;
        }

        @Override
        public Wait waitFor(final Context context, final BlockingRoundJoin roundJoin) {
            return Wait.until(branchCompleted.forBranches(
                    roundJoin.joinRound(), roundJoin.blockingRound().requiredSignals()));
        }

        @Override
        public StepDecision execute(final Context context, final BlockingRoundJoin roundJoin) {
            final BlockingJoinRound blockingRound = roundJoin.blockingRound();
            final List<String> fired = branchCompleted
                    .getBranchResults(context, roundJoin.joinRound())
                    .stream()
                    .sorted()
                    .toList();
            final StepMovement<ProcessedRound> processRound = StepMovement.of(
                    ProcessRound.class, new ProcessedRound(blockingRound.round(), fired));
            if (blockingRound.round() >= LAST_ROUND) {
                return StepDecision.goToMany(processRound);
            }
            return StepDecision.goToMany(
                    processRound,
                    StepMovement.of(
                            StartBlockingRound.class,
                            new BlockingJoinRound(
                                    blockingRound.requiredSignals(),
                                    blockingRound.round() + 1,
                                    roundJoin.joinRound())));
        }
    }

    static final class ProcessRound implements Step<ProcessedRound> {
        @Override
        public Class<ProcessedRound> getInputType() {
            return ProcessedRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final ProcessedRound processedRound) {
            return StepDecision.gracefulComplete(processedRound);
        }
    }
}
