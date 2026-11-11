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

import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;

/**
 * WCP-35 Cancelling Partial Join for Multiple Instances: the join fires after the required
 * inspections and cancels the remaining instances.
 */
@Component
public class CancellingPartialJoinForMultipleInstancesFlow
        implements Flow<CancellingPartialJoinForMultipleInstancesFlow.QuorumInspectionPlan> {
    public static final Channel<String> quorumInspectionReleased =
            Channel.define("quorumInspectionReleased", String.class);
    public static final Join<Integer> quorumUnitInspected =
            Join.define("quorumUnitInspected", Integer.class);
    public static final Channel<String> quorumCertified =
            Channel.define("quorumCertified", String.class);

    /** How many units to inspect and how many inspections form a quorum. */
    public record QuorumInspectionPlan(int inspectionCount, int requiredInspections) {
    }

    /** One inspection instance: its unit index and the join round it reports to. */
    public record QuorumUnitInspection(int unitIndex, JoinRound round) {
    }

    /** The quorum join: the inspections it needs and the round it waits on. */
    public record QuorumJoin(int requiredInspections, JoinRound round) {
    }

    private final StartQuorumInspections startQuorumInspections = new StartQuorumInspections();
    private final InspectQuorumUnit inspectQuorumUnit = new InspectQuorumUnit();
    private final AcceptQuorumInspections acceptQuorumInspections = new AcceptQuorumInspections();
    private final CertifyQuorum certifyQuorum = new CertifyQuorum();

    @Override
    public StepList<QuorumInspectionPlan> getSteps() {
        return StepList.startStep(startQuorumInspections)
                .otherSteps(inspectQuorumUnit, acceptQuorumInspections, certifyQuorum);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(quorumInspectionReleased, quorumUnitInspected, quorumCertified);
    }

    /** Counts unconsumed releases, which proves no late instance ran. */
    @RPC(loadChannels = {"quorumInspectionReleased"})
    public RPCResult<Integer> getQueuedQuorumReleaseCount(final Context context) {
        return RPCResult.of(quorumInspectionReleased.size(context));
    }

    @RPC
    public void certifyQuorum(final Context context) {
        quorumCertified.publish(context, "certified");
    }

    @RPC
    public void releaseQuorumInspections(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            quorumInspectionReleased.publish(context, "released");
        }
    }

    static final class StartQuorumInspections implements Step<QuorumInspectionPlan> {
        @Override
        public Class<QuorumInspectionPlan> getInputType() {
            return QuorumInspectionPlan.class;
        }

        @Override
        public StepDecision execute(final Context context, final QuorumInspectionPlan plan) {
            final List<Function<JoinRound, StepMovement<?>>> inspections =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            for (int index = 0; index < plan.inspectionCount(); index++) {
                final int unitIndex = index;
                inspections.add(round -> StepMovement.of(
                        InspectQuorumUnit.class, new QuorumUnitInspection(unitIndex, round)));
            }
            return StepDecision.goToMany(quorumUnitInspected.fork(
                    context,
                    inspections,
                    round -> StepMovement.of(
                            AcceptQuorumInspections.class,
                            new QuorumJoin(plan.requiredInspections(), round))));
        }
    }

    static final class InspectQuorumUnit implements Step<QuorumUnitInspection> {
        @Override
        public Class<QuorumUnitInspection> getInputType() {
            return QuorumUnitInspection.class;
        }

        @Override
        public Wait waitFor(final Context context, final QuorumUnitInspection unit) {
            return Wait.until(quorumInspectionReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final QuorumUnitInspection unit) {
            return quorumUnitInspected.endBranch(context, unit.round(), unit.unitIndex());
        }
    }

    static final class AcceptQuorumInspections implements Step<QuorumJoin> {
        @Override
        public Class<QuorumJoin> getInputType() {
            return QuorumJoin.class;
        }

        @Override
        public Wait waitFor(final Context context, final QuorumJoin quorumJoin) {
            return Wait.until(quorumUnitInspected.forBranches(
                    quorumJoin.round(), quorumJoin.requiredInspections()));
        }

        @Override
        @SuppressWarnings("unchecked")
        public StepDecision execute(final Context context, final QuorumJoin quorumJoin) {
            final Integer[] unitIndexes = quorumUnitInspected
                    .getBranchResults(context, quorumJoin.round())
                    .stream()
                    .sorted()
                    .toArray(Integer[]::new);
            return StepDecision.goTo(CertifyQuorum.class, unitIndexes)
                    .withCancelingSiblingSteps(InspectQuorumUnit.class);
        }
    }

    static final class CertifyQuorum implements Step<Integer[]> {
        @Override
        public Class<Integer[]> getInputType() {
            return Integer[].class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer[] unitIndexes) {
            return Wait.until(quorumCertified.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer[] unitIndexes) {
            return StepDecision.gracefulComplete(unitIndexes);
        }
    }
}
