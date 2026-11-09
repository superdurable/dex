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

import java.util.List;

/**
 * WCP-34 Static Partial Join for Multiple Instances: the join fires after the required
 * inspections, and a reset Step absorbs the remaining instances.
 */
@Component
public class StaticPartialJoinForMultipleInstancesFlow
        implements Flow<StaticPartialJoinForMultipleInstancesFlow.InspectionPlan> {
    public static final Channel<String> inspectionReleased =
            Channel.define("inspectionReleased", String.class);
    public static final Channel<Integer> unitInspected =
            Channel.define("unitInspected", Integer.class);

    /** How many units to inspect and how many inspections approve the batch. */
    public record InspectionPlan(int inspectionCount, int requiredInspections) {
    }

    /** The join thresholds: the inspections to accept and the inspections to absorb afterwards. */
    public record InspectionJoin(int requiredInspections, int remainingInspections) {
    }

    private final StartInspections startInspections = new StartInspections();
    private final InspectUnit inspectUnit = new InspectUnit();
    private final AcceptInspections acceptInspections = new AcceptInspections();
    private final ApproveBatch approveBatch = new ApproveBatch();
    private final ResetInspectionJoin resetInspectionJoin = new ResetInspectionJoin();

    @Override
    public StepList<InspectionPlan> getSteps() {
        return StepList.startStep(startInspections)
                .otherSteps(inspectUnit, acceptInspections, approveBatch, resetInspectionJoin);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(inspectionReleased, unitInspected);
    }

    @RPC
    public void releaseInspections(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            inspectionReleased.publish(context, "released");
        }
    }

    static final class StartInspections implements Step<InspectionPlan> {
        @Override
        public Class<InspectionPlan> getInputType() {
            return InspectionPlan.class;
        }

        @Override
        public StepDecision execute(final Context context, final InspectionPlan plan) {
            final StepMovement<?>[] movements = new StepMovement<?>[plan.inspectionCount() + 1];
            for (int index = 0; index < plan.inspectionCount(); index++) {
                movements[index] = StepMovement.of(InspectUnit.class, index);
            }
            movements[plan.inspectionCount()] = StepMovement.of(
                    AcceptInspections.class,
                    new InspectionJoin(
                            plan.requiredInspections(),
                            plan.inspectionCount() - plan.requiredInspections()));
            return StepDecision.goToMany(movements);
        }
    }

    static final class InspectUnit implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer unitIndex) {
            return Wait.until(inspectionReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer unitIndex) {
            unitInspected.publish(context, unitIndex);
            return StepDecision.deadEnd();
        }
    }

    static final class AcceptInspections implements Step<InspectionJoin> {
        @Override
        public Class<InspectionJoin> getInputType() {
            return InspectionJoin.class;
        }

        @Override
        public Wait waitFor(final Context context, final InspectionJoin join) {
            return Wait.until(unitInspected.forN(join.requiredInspections()));
        }

        @Override
        public StepDecision execute(final Context context, final InspectionJoin join) {
            final List<Integer> inspected = unitInspected.getConditionResults(context);
            return StepDecision.goToMany(
                    StepMovement.of(ApproveBatch.class, inspected.toArray(new Integer[0])),
                    StepMovement.of(ResetInspectionJoin.class, join.remainingInspections()));
        }
    }

    static final class ApproveBatch implements Step<Integer[]> {
        @Override
        public Class<Integer[]> getInputType() {
            return Integer[].class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer[] unitIndexes) {
            return StepDecision.gracefulComplete(List.of(unitIndexes).stream().sorted().toList());
        }
    }

    static final class ResetInspectionJoin implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer remainingInspections) {
            return Wait.until(unitInspected.forN(remainingInspections));
        }

        @Override
        public StepDecision execute(final Context context, final Integer remainingInspections) {
            return StepDecision.gracefulComplete(
                    unitInspected.getConditionResults(context).stream().sorted().toList());
        }
    }
}
