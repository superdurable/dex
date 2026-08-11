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

package io.superdurable.dex.workflow.retryingfailure;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public final class RetryingFailureFlow implements Flow<Void> {
    private static final Duration RETRY_INTERVAL = Duration.ofMinutes(10);
    private static final int MAXIMUM_ATTEMPTS = 100;

    private final RetryingExecuteStep start = new RetryingExecuteStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(start);
    }

    static final class RetryingExecuteStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            throw new IllegalStateException(
                    "Java Execute failed; next retry is scheduled in 10 minutes");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(RETRY_INTERVAL)
                            .backoffCoefficient(1.0)
                            .maximumInterval(RETRY_INTERVAL)
                            .maximumAttempts(MAXIMUM_ATTEMPTS)
                            .build())
                    .executeDurability(StepDurability.SYNC)
                    .build();
        }
    }
}
