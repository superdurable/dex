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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
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
import java.util.function.Function;

/** WCP-34: the join fires after the required inspections; the next split resets it. */
@Component
public class StaticPartialJoinForMultipleInstancesFlow
        implements Flow<StaticPartialJoinForMultipleInstancesFlow.InspectionRound> {
    public static final Channel<String> inspectionReleased =
            Channel.define("inspectionReleased", String.class);
    public static final Join<Integer> unitInspected = Join.define("unitInspected", Integer.class);
    public static final Attribute<InspectedUnits> ignoredInspections =
            Attribute.define("ignoredInspections", InspectedUnits.class);

    /**
     * One join round: the instances, the inspections it needs, its number, the round count, and
     * the previous Join round, which is null in the first round.
     */
    public record InspectionRound(
            int inspectionCount,
            int requiredInspections,
            int round,
            int roundCount,
            JoinRound previousRound) {
        /** Creates the first round, which has no previous Join round. */
        public InspectionRound(
                final int inspectionCount,
                final int requiredInspections,
                final int round,
                final int roundCount) {
            this(inspectionCount, requiredInspections, round, roundCount, null);
        }
    }

    /** One instance of a round: its unit index and the Join round it reports to. */
    public record UnitInspection(int unitIndex, JoinRound round) {
    }

    /** The join Step input: the inspection round and the Join round it waits on. */
    public record InspectionJoin(InspectionRound inspectionRound, JoinRound joinRound) {
    }

    /** The unit indexes a join or reset consumed in one round. */
    public record InspectedUnits(int round, List<Integer> unitIndexes) {
    }

    private final StartInspections startInspections = new StartInspections();
    private final InspectUnit inspectUnit = new InspectUnit();
    private final AcceptInspections acceptInspections = new AcceptInspections();
    private final ApproveBatch approveBatch = new ApproveBatch();

    @Override
    public StepList<InspectionRound> getSteps() {
        return StepList.startStep(startInspections)
                .otherSteps(inspectUnit, acceptInspections, approveBatch);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(inspectionReleased, unitInspected, ignoredInspections);
    }

    @RPC
    public void releaseInspections(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            inspectionReleased.publish(context, "released");
        }
    }

    static final class StartInspections implements Step<InspectionRound> {
        @Override
        public Class<InspectionRound> getInputType() {
            return InspectionRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final InspectionRound inspectionRound) {
            if (inspectionRound.previousRound() == null) {
                return Wait.skipImmediately();
            }
            return Wait.until(unitInspected.forRemainingBranches(
                    inspectionRound.previousRound(), inspectionRound.requiredInspections()));
        }

        @Override
        public StepDecision execute(final Context context, final InspectionRound inspectionRound) {
            if (inspectionRound.previousRound() != null) {
                ignoredInspections.set(context, new InspectedUnits(
                        inspectionRound.round() - 1,
                        unitInspected
                                .getRemainingBranchResults(context, inspectionRound.previousRound())
                                .stream()
                                .sorted()
                                .toList()));
            }
            final List<Function<JoinRound, StepMovement<?>>> inspections =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            for (int index = 0; index < inspectionRound.inspectionCount(); index++) {
                final int unitIndex = index;
                inspections.add(round ->
                        StepMovement.of(InspectUnit.class, new UnitInspection(unitIndex, round)));
            }
            return StepDecision.goToMany(unitInspected.fork(
                    context,
                    inspections,
                    round -> StepMovement.of(
                            AcceptInspections.class, new InspectionJoin(inspectionRound, round))));
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
            return unitInspected.endBranch(context, unit.round(), unit.unitIndex());
        }
    }

    static final class AcceptInspections implements Step<InspectionJoin> {
        @Override
        public Class<InspectionJoin> getInputType() {
            return InspectionJoin.class;
        }

        @Override
        public Wait waitFor(final Context context, final InspectionJoin inspectionJoin) {
            return Wait.until(unitInspected.forBranches(
                    inspectionJoin.joinRound(),
                    inspectionJoin.inspectionRound().requiredInspections()));
        }

        @Override
        public StepDecision execute(final Context context, final InspectionJoin inspectionJoin) {
            final InspectionRound inspectionRound = inspectionJoin.inspectionRound();
            final InspectedUnits approved = new InspectedUnits(
                    inspectionRound.round(),
                    unitInspected.getBranchResults(context, inspectionJoin.joinRound()).stream()
                            .sorted()
                            .toList());
            final StepMovement<InspectedUnits> approveBatch =
                    StepMovement.of(ApproveBatch.class, approved);
            if (inspectionRound.round() >= inspectionRound.roundCount()) {
                // The last round's late inspections stay pending until the Flow closes.
                return StepDecision.goToMany(approveBatch);
            }
            return StepDecision.goToMany(
                    approveBatch,
                    StepMovement.of(
                            StartInspections.class,
                            new InspectionRound(
                                    inspectionRound.inspectionCount(),
                                    inspectionRound.requiredInspections(),
                                    inspectionRound.round() + 1,
                                    inspectionRound.roundCount(),
                                    inspectionJoin.joinRound())));
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
}
