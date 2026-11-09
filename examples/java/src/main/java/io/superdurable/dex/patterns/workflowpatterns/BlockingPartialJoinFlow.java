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
import io.superdurable.dex.Condition;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

/** WCP-28 and WCP-31: later signals stay queued until the reset Step re-enables the join. */
@Component
public class BlockingPartialJoinFlow implements Flow<BlockingPartialJoinFlow.BlockingJoinRound> {
    public static final Channel<String> branchASignal = Channel.define("branchASignal", String.class);
    public static final Channel<String> branchBSignal = Channel.define("branchBSignal", String.class);
    public static final Channel<String> branchCSignal = Channel.define("branchCSignal", String.class);
    private static final List<String> BRANCHES = List.of("A", "B", "C");
    private static final int LAST_ROUND = 2;

    /** The input of one BlockingJoin round. */
    public record BlockingJoinRound(int requiredSignals, int round) {
    }

    /** The branches whose signals fired the join in one round. */
    public record ProcessedRound(int round, List<String> branches) {
    }

    /** The branches the reset Step must absorb before the next round. */
    public record PendingBranches(int requiredSignals, int round, List<String> pendingBranches) {
    }

    private final BlockingJoin blockingJoin = new BlockingJoin();
    private final ProcessRound processRound = new ProcessRound();
    private final ResetBlockingJoin resetBlockingJoin = new ResetBlockingJoin();

    @Override
    public StepList<BlockingJoinRound> getSteps() {
        return StepList.startStep(blockingJoin).otherSteps(processRound, resetBlockingJoin);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(branchASignal, branchBSignal, branchCSignal);
    }

    /** Sends the signal of branch "A", "B", or "C". */
    @RPC
    public void sendBranchSignal(final Context context, final String branch) {
        branchSignalChannel(branch).publish(context, branch);
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

    static final class BlockingJoin implements Step<BlockingJoinRound> {
        @Override
        public Class<BlockingJoinRound> getInputType() {
            return BlockingJoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final BlockingJoinRound joinRound) {
            if (joinRound.requiredSignals() == 1) {
                return Wait.anyOf(
                        branchASignal.forOne(), branchBSignal.forOne(), branchCSignal.forOne());
            }
            return Wait.anyCombinationOf(
                    ConditionCombination.of(
                            branchASignal.forOne("ab-a"), branchBSignal.forOne("ab-b")),
                    ConditionCombination.of(
                            branchASignal.forOne("ac-a"), branchCSignal.forOne("ac-c")),
                    ConditionCombination.of(
                            branchBSignal.forOne("bc-b"), branchCSignal.forOne("bc-c")));
        }

        @Override
        public StepDecision execute(final Context context, final BlockingJoinRound joinRound) {
            final List<String> fired = new ArrayList<String>();
            final List<String> pending = new ArrayList<String>();
            for (final String branch : BRANCHES) {
                if (branchSignalChannel(branch).getConditionResults(context).isEmpty()) {
                    pending.add(branch);
                } else {
                    fired.add(branch);
                }
            }
            return StepDecision.goToMany(
                    StepMovement.of(
                            ProcessRound.class, new ProcessedRound(joinRound.round(), fired)),
                    StepMovement.of(
                            ResetBlockingJoin.class,
                            new PendingBranches(
                                    joinRound.requiredSignals(), joinRound.round(), pending)));
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

    static final class ResetBlockingJoin implements Step<PendingBranches> {
        @Override
        public Class<PendingBranches> getInputType() {
            return PendingBranches.class;
        }

        @Override
        public Wait waitFor(final Context context, final PendingBranches pending) {
            final Condition[] conditions = pending.pendingBranches().stream()
                    .map(branch -> branchSignalChannel(branch).forOne())
                    .toArray(Condition[]::new);
            return Wait.allOf(conditions);
        }

        @Override
        public StepDecision execute(final Context context, final PendingBranches pending) {
            if (pending.round() < LAST_ROUND) {
                return StepDecision.goTo(
                        BlockingJoin.class,
                        new BlockingJoinRound(pending.requiredSignals(), pending.round() + 1));
            }
            return StepDecision.gracefulComplete(pending.round());
        }
    }
}
