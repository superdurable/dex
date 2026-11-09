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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-42 Thread Split and WCP-41 Thread Merge: k executions of one Step type, then one join. */
@Component
public class ThreadSplitMergeFlow implements Flow<Integer> {
    public static final Channel<Integer> parcelPacked = Channel.define("parcelPacked", Integer.class);

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
            final StepMovement<?>[] movements = new StepMovement<?>[parcelCount + 1];
            for (int index = 0; index < parcelCount; index++) {
                movements[index] = StepMovement.of(PackParcel.class, index);
            }
            movements[parcelCount] = StepMovement.of(MergeParcels.class, parcelCount);
            return StepDecision.goToMany(movements);
        }
    }

    static final class PackParcel implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer parcelIndex) {
            parcelPacked.publish(context, parcelIndex);
            return StepDecision.deadEnd();
        }
    }

    static final class MergeParcels implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer parcelCount) {
            return Wait.until(parcelPacked.forN(parcelCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer parcelCount) {
            return StepDecision.gracefulComplete(
                    parcelPacked.getConditionResults(context).stream().sorted().toList());
        }
    }
}
