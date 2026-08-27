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

package io.superdurable.dex.patterns.intervention;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class ManualRecoveryFlow implements Flow<Boolean> {
    public static final String RETRY_CHANNEL = "manual-recovery-retry";
    public static final String SKIP_CHANNEL = "manual-recovery-skip";

    public final Channel<Void> retryChannel =
            Channel.define(RETRY_CHANNEL, Void.class);
    public final Channel<Void> skipChannel =
            Channel.define(SKIP_CHANNEL, Void.class);

    private final DoWorkStep doWorkStep = new DoWorkStep();
    private final ManualStep manualStep = new ManualStep();

    @Override
    public StepList<Boolean> getSteps() {
        return StepList.startStep(doWorkStep).otherSteps(manualStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(retryChannel, skipChannel);
    }

    final class DoWorkStep implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(Duration.ofSeconds(1))
                            .backoffCoefficient(2.0)
                            .maximumInterval(Duration.ofSeconds(4))
                            .maximumAttempts(4)
                            .build())
                    .onExecuteFailureProceedTo(ManualStep.class)
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final Boolean shouldFail) {
            if (Boolean.TRUE.equals(shouldFail)) {
                throw new IllegalStateException("work failed");
            }
            return StepDecision.gracefulComplete("work completed");
        }
    }

    final class ManualStep implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public Wait waitFor(final Context context, final Boolean input) {
            return Wait.anyOf(retryChannel.forOne(), skipChannel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Boolean input) {
            if (!retryChannel.getConditionResults(context).isEmpty()) {
                return StepDecision.goTo(DoWorkStep.class, false);
            }
            return StepDecision.forceFail("manual recovery skipped");
        }
    }
}
