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

package io.superdurable.dex.patterns.polling;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class SimplePollingFlow implements Flow<Void> {
    private final SimplePolling simplePolling = new SimplePolling();
    private final SimplePollingComplete simplePollingComplete = new SimplePollingComplete();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(simplePolling).otherSteps(simplePollingComplete);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class SimplePolling implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(Timer.byDuration(Duration.ofSeconds(10)));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (isSystemReady()) {
                return StepDecision.goTo(SimplePollingComplete.class, null);
            }
            return StepDecision.goTo(SimplePolling.class, null);
        }

        private boolean isSystemReady() {
            System.out.println("Executing external system check for readiness...");
            return true;
        }
    }

    final class SimplePollingComplete implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            System.out.println("Executing final state to complete the workflow...");
            return StepDecision.gracefulComplete();
        }
    }
}
