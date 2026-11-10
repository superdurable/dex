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

package io.superdurable.dex.patterns.cancelcase;

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

/** The child Flow that {@link LoanApplicationFlow} starts. */
@Component
public class CreditCheckFlow implements Flow<CreditCheckFlow.CreditCheckRequest> {
    /**
     * The input of one credit check child Flow.
     *
     * @param reportDelaySeconds simulates how long the credit bureau takes to answer
     */
    public record CreditCheckRequest(String applicantId, int reportDelaySeconds) {
    }

    private final AwaitCreditReport awaitCreditReport = new AwaitCreditReport();

    @Override
    public StepList<CreditCheckRequest> getSteps() {
        return StepList.startStep(awaitCreditReport);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    /** Waits for the simulated credit bureau to answer. */
    static final class AwaitCreditReport implements Step<CreditCheckRequest> {
        @Override
        public Class<CreditCheckRequest> getInputType() {
            return CreditCheckRequest.class;
        }

        @Override
        public Wait waitFor(final Context context, final CreditCheckRequest request) {
            return Wait.until(Timer.byDuration(Duration.ofSeconds(request.reportDelaySeconds())));
        }

        @Override
        public StepDecision execute(final Context context, final CreditCheckRequest request) {
            return StepDecision.gracefulComplete("credit report for " + request.applicantId());
        }
    }
}
