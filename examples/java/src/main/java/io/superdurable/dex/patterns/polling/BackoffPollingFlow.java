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
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.exceptions.RetryAfterException;
import io.superdurable.dex.shared.ServiceDependency;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class BackoffPollingFlow implements Flow<Void> {
    private final ServiceDependency service;
    private final PollingStep pollingStep = new PollingStep();

    public BackoffPollingFlow(final ServiceDependency service) {
        this.service = service;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(pollingStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class PollingStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public String getStepType() { return "PollingStep"; }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .backoffCoefficient(2.0)
                            .maximumAttempts(5)
                            .totalDuration(Duration.ofSeconds(3600))
                            .initialInterval(Duration.ofSeconds(3))
                            .maximumInterval(Duration.ofSeconds(60))
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            try {
                return StepDecision.gracefulComplete(
                        service.attemptExternalApiCall("Poll for BackoffPollingFlow"));
            } catch (final RuntimeException error) {
                throw RetryAfterException.after(Duration.ofSeconds(1), error);
            }
        }
    }
}
