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

package io.superdurable.dex.primitives.customretry;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.exceptions.RetryAfterException;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public final class CustomRetryFlow implements Flow<Integer> {
    private final CustomRetryStep start = new CustomRetryStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class CustomRetryStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer readyAfterAttempt) {
            if (context.getAttempt() < readyAfterAttempt) {
                final IllegalStateException cause =
                        new IllegalStateException("not ready on attempt " + context.getAttempt());
                throw RetryAfterException.after(Duration.ofSeconds(7), cause);
            }
            return StepDecision.gracefulComplete("ready");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder().maximumAttempts(5).build())
                    .build();
        }
    }
}
