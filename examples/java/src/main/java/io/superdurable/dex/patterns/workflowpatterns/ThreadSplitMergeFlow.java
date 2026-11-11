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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;

/** WCP-42 Thread Split and WCP-41 Thread Merge: k executions of one Step type, then one join. */
@Component
public class ThreadSplitMergeFlow implements Flow<Integer> {
    public static final Join<Integer> parcelPacked = Join.define("parcelPacked", Integer.class);

    /** One parcel thread: its index and the join round it reports to. */
    public record ParcelThread(int parcelIndex, JoinRound round) {
    }

    private final SplitShipment splitShipment = new SplitShipment();
    private final PackParcel packParcel = new PackParcel();
    private final MergeParcels mergeParcels = new MergeParcels();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(splitShipment).otherSteps(packParcel, mergeParcels);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(parcelPacked);
    }

    static final class SplitShipment implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer parcelCount) {
            final List<Function<JoinRound, StepMovement<?>>> threads =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            for (int index = 0; index < parcelCount; index++) {
                final int parcelIndex = index;
                threads.add(round ->
                        StepMovement.of(PackParcel.class, new ParcelThread(parcelIndex, round)));
            }
            return StepDecision.goToMany(parcelPacked.fork(
                    context, threads, round -> StepMovement.of(MergeParcels.class, round)));
        }
    }

    static final class PackParcel implements Step<ParcelThread> {
        @Override
        public Class<ParcelThread> getInputType() {
            return ParcelThread.class;
        }

        @Override
        public StepDecision execute(final Context context, final ParcelThread thread) {
            return parcelPacked.endBranch(context, thread.round(), thread.parcelIndex());
        }
    }

    static final class MergeParcels implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(parcelPacked.forAllBranches(round));
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return StepDecision.gracefulComplete(
                    parcelPacked.getBranchResults(context, round).stream().sorted().toList());
        }
    }
}
