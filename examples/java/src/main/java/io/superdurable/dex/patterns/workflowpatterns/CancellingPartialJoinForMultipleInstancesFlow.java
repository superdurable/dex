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
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/**
 * WCP-35 Cancelling Partial Join for Multiple Instances: the join fires after the required
 * inspections and cancels the remaining instances.
 */
@Component
public class CancellingPartialJoinForMultipleInstancesFlow
        implements Flow<CancellingPartialJoinForMultipleInstancesFlow.QuorumInspectionPlan> {
    public static final Channel<String> quorumInspectionReleased =
            Channel.define("quorumInspectionReleased", String.class);
    public static final Channel<Integer> quorumUnitInspected =
            Channel.define("quorumUnitInspected", Integer.class);

    /** How many units to inspect and how many inspections form a quorum. */
    public record QuorumInspectionPlan(int inspectionCount, int requiredInspections) {
    }

    private final StartQuorumInspections startQuorumInspections = new StartQuorumInspections();
    private final InspectQuorumUnit inspectQuorumUnit = new InspectQuorumUnit();
    private final AcceptQuorumInspections acceptQuorumInspections = new AcceptQuorumInspections();

    @Override
    public StepList<QuorumInspectionPlan> getSteps() {
        return StepList.startStep(startQuorumInspections)
                .otherSteps(inspectQuorumUnit, acceptQuorumInspections);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(quorumInspectionReleased, quorumUnitInspected);
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
            final StepMovement<?>[] movements = new StepMovement<?>[plan.inspectionCount() + 1];
            for (int index = 0; index < plan.inspectionCount(); index++) {
                movements[index] = StepMovement.of(InspectQuorumUnit.class, index);
            }
            movements[plan.inspectionCount()] =
                    StepMovement.of(AcceptQuorumInspections.class, plan.requiredInspections());
            return StepDecision.goToMany(movements);
        }
    }

    static final class InspectQuorumUnit implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer unitIndex) {
            return Wait.until(quorumInspectionReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer unitIndex) {
            quorumUnitInspected.publish(context, unitIndex);
            return StepDecision.deadEnd();
        }
    }

    static final class AcceptQuorumInspections implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer requiredInspections) {
            return Wait.until(quorumUnitInspected.forN(requiredInspections));
        }

        @Override
        @SuppressWarnings("unchecked")
        public StepDecision execute(final Context context, final Integer requiredInspections) {
            return StepDecision.gracefulComplete(
                            quorumUnitInspected.getConditionResults(context).stream().sorted().toList())
                    .withCancelingSteps(InspectQuorumUnit.class);
        }
    }
}
