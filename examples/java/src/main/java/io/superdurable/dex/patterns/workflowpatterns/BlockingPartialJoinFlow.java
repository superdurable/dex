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
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
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

/** WCP-28 and WCP-31: no Step waits on round k + 1 signals until round k resets. */
@Component
public class BlockingPartialJoinFlow implements Flow<BlockingPartialJoinFlow.BlockingJoinRound> {
    public static final Channel<String> branchASignal = Channel.define("branchASignal", String.class);
    public static final Channel<String> branchBSignal = Channel.define("branchBSignal", String.class);
    public static final Channel<String> branchCSignal = Channel.define("branchCSignal", String.class);
    public static final ChannelMap<String> branchCompleted =
            ChannelMap.define("branchCompleted", String.class);
    private static final int BRANCH_COUNT = 3;
    private static final int LAST_ROUND = 2;

    /** One join round: the signals it needs and its number. */
    public record BlockingJoinRound(int requiredSignals, int round) {
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
    private final ResetBlockingJoin resetBlockingJoin = new ResetBlockingJoin();

    @Override
    public StepList<BlockingJoinRound> getSteps() {
        return StepList.startStep(startBlockingRound).otherSteps(
                signalFromBranchA,
                signalFromBranchB,
                signalFromBranchC,
                blockingJoin,
                processRound,
                resetBlockingJoin);
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

    private static StepDecision completeBranch(
            final Context context, final int round, final String branch) {
        branchCompleted.publish(context, JoinRoundInstance.of(round), branch);
        return StepDecision.deadEnd();
    }

    static final class StartBlockingRound implements Step<BlockingJoinRound> {
        @Override
        public Class<BlockingJoinRound> getInputType() {
            return BlockingJoinRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final BlockingJoinRound joinRound) {
            return StepDecision.goToMany(
                    StepMovement.of(SignalFromBranchA.class, joinRound.round()),
                    StepMovement.of(SignalFromBranchB.class, joinRound.round()),
                    StepMovement.of(SignalFromBranchC.class, joinRound.round()),
                    StepMovement.of(BlockingJoin.class, joinRound));
        }
    }

    static final class SignalFromBranchA implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer round) {
            return Wait.until(branchASignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer round) {
            return completeBranch(context, round, "A");
        }
    }

    static final class SignalFromBranchB implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer round) {
            return Wait.until(branchBSignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer round) {
            return completeBranch(context, round, "B");
        }
    }

    static final class SignalFromBranchC implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer round) {
            return Wait.until(branchCSignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer round) {
            return completeBranch(context, round, "C");
        }
    }

    static final class BlockingJoin implements Step<BlockingJoinRound> {
        @Override
        public Class<BlockingJoinRound> getInputType() {
            return BlockingJoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final BlockingJoinRound joinRound) {
            return Wait.until(branchCompleted.forN(
                    JoinRoundInstance.of(joinRound.round()), joinRound.requiredSignals()));
        }

        @Override
        public StepDecision execute(final Context context, final BlockingJoinRound joinRound) {
            final List<String> fired = branchCompleted
                    .getConditionResults(context, JoinRoundInstance.of(joinRound.round()))
                    .stream()
                    .sorted()
                    .toList();
            return StepDecision.goToMany(
                    StepMovement.of(
                            ProcessRound.class, new ProcessedRound(joinRound.round(), fired)),
                    StepMovement.of(ResetBlockingJoin.class, joinRound));
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

    static final class ResetBlockingJoin implements Step<BlockingJoinRound> {
        @Override
        public Class<BlockingJoinRound> getInputType() {
            return BlockingJoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final BlockingJoinRound joinRound) {
            return Wait.until(branchCompleted.forN(
                    JoinRoundInstance.of(joinRound.round()),
                    BRANCH_COUNT - joinRound.requiredSignals()));
        }

        @Override
        public StepDecision execute(final Context context, final BlockingJoinRound joinRound) {
            if (joinRound.round() < LAST_ROUND) {
                return StepDecision.goTo(
                        StartBlockingRound.class,
                        new BlockingJoinRound(joinRound.requiredSignals(), joinRound.round() + 1));
            }
            return StepDecision.gracefulComplete(joinRound.round());
        }
    }
}
