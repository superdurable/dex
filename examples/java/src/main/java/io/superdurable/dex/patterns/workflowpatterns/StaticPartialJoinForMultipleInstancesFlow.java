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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

/** WCP-34: the join fires after the required inspections; a reset Step re-enables it. */
@Component
public class StaticPartialJoinForMultipleInstancesFlow
        implements Flow<StaticPartialJoinForMultipleInstancesFlow.InspectionRound> {
    public static final Channel<String> inspectionReleased =
            Channel.define("inspectionReleased", String.class);
    public static final ChannelMap<Integer> unitInspected =
            ChannelMap.define("unitInspected", Integer.class);

    /** One join round: the instances, the inspections it needs, its number, and the round count. */
    public record InspectionRound(
            int inspectionCount, int requiredInspections, int round, int roundCount) {
    }

    /** One instance of a round. */
    public record UnitInspection(int round, int unitIndex) {
    }

    /** The unit indexes a join or reset consumed in one round. */
    public record InspectedUnits(int round, List<Integer> unitIndexes) {
    }

    private final StartInspections startInspections = new StartInspections();
    private final InspectUnit inspectUnit = new InspectUnit();
    private final AcceptInspections acceptInspections = new AcceptInspections();
    private final ApproveBatch approveBatch = new ApproveBatch();
    private final ResetInspectionJoin resetInspectionJoin = new ResetInspectionJoin();

    @Override
    public StepList<InspectionRound> getSteps() {
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

    private static List<Integer> sortedRoundIndexes(final Context context, final int round) {
        return unitInspected.getConditionResults(context, JoinRoundInstance.of(round)).stream()
                .sorted()
                .toList();
    }

    static final class StartInspections implements Step<InspectionRound> {
        @Override
        public Class<InspectionRound> getInputType() {
            return InspectionRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final InspectionRound inspectionRound) {
            final int count = inspectionRound.inspectionCount();
            final StepMovement<?>[] movements = new StepMovement<?>[count + 1];
            for (int index = 0; index < count; index++) {
                movements[index] = StepMovement.of(
                        InspectUnit.class, new UnitInspection(inspectionRound.round(), index));
            }
            movements[count] = StepMovement.of(AcceptInspections.class, inspectionRound);
            return StepDecision.goToMany(movements);
        }
    }

    static final class InspectUnit implements Step<UnitInspection> {
        @Override
        public Class<UnitInspection> getInputType() {
            return UnitInspection.class;
        }

        @Override
        public Wait waitFor(final Context context, final UnitInspection unit) {
            return Wait.until(inspectionReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final UnitInspection unit) {
            unitInspected.publish(context, JoinRoundInstance.of(unit.round()), unit.unitIndex());
            return StepDecision.deadEnd();
        }
    }

    static final class AcceptInspections implements Step<InspectionRound> {
        @Override
        public Class<InspectionRound> getInputType() {
            return InspectionRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final InspectionRound inspectionRound) {
            return Wait.until(unitInspected.forN(
                    JoinRoundInstance.of(inspectionRound.round()),
                    inspectionRound.requiredInspections()));
        }

        @Override
        public StepDecision execute(final Context context, final InspectionRound inspectionRound) {
            final InspectedUnits approved = new InspectedUnits(
                    inspectionRound.round(), sortedRoundIndexes(context, inspectionRound.round()));
            return StepDecision.goToMany(
                    StepMovement.of(ApproveBatch.class, approved),
                    StepMovement.of(ResetInspectionJoin.class, inspectionRound));
        }
    }

    static final class ApproveBatch implements Step<InspectedUnits> {
        @Override
        public Class<InspectedUnits> getInputType() {
            return InspectedUnits.class;
        }

        @Override
        public StepDecision execute(final Context context, final InspectedUnits approved) {
            return StepDecision.gracefulComplete(approved);
        }
    }

    static final class ResetInspectionJoin implements Step<InspectionRound> {
        @Override
        public Class<InspectionRound> getInputType() {
            return InspectionRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final InspectionRound inspectionRound) {
            return Wait.until(unitInspected.forN(
                    JoinRoundInstance.of(inspectionRound.round()),
                    inspectionRound.inspectionCount() - inspectionRound.requiredInspections()));
        }

        @Override
        public StepDecision execute(final Context context, final InspectionRound inspectionRound) {
            if (inspectionRound.round() < inspectionRound.roundCount()) {
                return StepDecision.goTo(
                        StartInspections.class,
                        new InspectionRound(
                                inspectionRound.inspectionCount(),
                                inspectionRound.requiredInspections(),
                                inspectionRound.round() + 1,
                                inspectionRound.roundCount()));
            }
            return StepDecision.gracefulComplete(new InspectedUnits(
                    inspectionRound.round(), sortedRoundIndexes(context, inspectionRound.round())));
        }
    }
}
