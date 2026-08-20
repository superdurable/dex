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

package io.superdurable.dex.primitives.proceedonwaitfailure;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForFailurePolicy;
import org.springframework.stereotype.Component;

@Component
public final class ProceedOnWaitFailureFlow implements Flow<String> {
    private final FinishStep finish = new FinishStep();
    private final FailingWaitStep failingWait = new FailingWaitStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(failingWait).otherSteps(finish);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class FailingWaitStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .waitForRetry(RetryPolicy.newBuilder().maximumAttempts(2).build())
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            throw new IllegalStateException("planned WaitFor failure");
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            if (!context.waitForMethodFailed()) {
                throw new IllegalStateException("waitFor failure was not reported");
            }
            return StepDecision.goTo(finish, input + "_recovered");
        }
    }

    static final class FinishStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }
}
