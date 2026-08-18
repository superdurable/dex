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

package io.superdurable.dex.patterns.timeout;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class FlowGracefulTimeout implements Flow<Boolean> {
    private final Init init = new Init();
    private final Timeout timeout = new Timeout();
    private final Task task = new Task();

    @Override
    public StepList<Boolean> getSteps() {
        return StepList.startStep(init).otherSteps(timeout, task);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class Init implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public StepDecision execute(final Context context, final Boolean workflowSuccessful) {
            return StepDecision.goToMulti(
                    StepMovement.of(timeout, null),
                    StepMovement.of(task, workflowSuccessful));
        }
    }

    final class Timeout implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(Timer.byDuration(Duration.ofMinutes(1)));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.forceFail("Workflow did not finish the task in time");
        }
    }

    final class Task implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public Wait waitFor(final Context context, final Boolean workflowSuccessful) {
            if (workflowSuccessful) {
                return Wait.skipImmediately();
            }
            return Wait.until(Timer.byDuration(Duration.ofSeconds(65)));
        }

        @Override
        public StepDecision execute(final Context context, final Boolean workflowSuccessful) {
            return StepDecision.forceComplete("Workflow completed successfully");
        }
    }
}
