/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.durability;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public final class DurabilityFlow implements Flow<String> {
    private final RouteDurabilityStep route = new RouteDurabilityStep();
    private final SyncWorkStep syncWork = new SyncWorkStep();
    private final AsyncWorkStep asyncWork = new AsyncWorkStep();
    private final FinishDurabilityStep finish = new FinishDurabilityStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(route).otherSteps(syncWork, asyncWork, finish);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class RouteDurabilityStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String mode) {
            if ("async".equals(mode)) {
                return StepDecision.goTo(asyncWork, mode);
            }
            return StepDecision.goTo(syncWork, mode);
        }
    }

    final class SyncWorkStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String mode) {
            return StepDecision.goTo(finish, "sync:" + mode);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeDurability(StepDurability.SYNC)
                    .build();
        }
    }

    final class AsyncWorkStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String mode) {
            return StepDecision.goTo(finish, "async:" + mode);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeDurability(StepDurability.ASYNC)
                    .build();
        }
    }

    static final class FinishDurabilityStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String label) {
            return Wait.anyOf(Timer.byDuration(Duration.ofSeconds(1)));
        }

        @Override
        public StepDecision execute(final Context context, final String label) {
            return StepDecision.gracefulComplete(label);
        }
    }
}
