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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-22 Recursion: a Step waits for a SubFlow of its own Flow type. */
@Component
public class FactorialRecursionFlow implements Flow<Integer> {
    private final ComputeFactorial computeFactorial = new ComputeFactorial();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(computeFactorial);
    }

    static final class ComputeFactorial implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer n) {
            if (n <= 1) {
                return Wait.skipImmediately();
            }
            return Wait.until(SubFlow.run(FactorialRecursionFlow.class, n - 1));
        }

        @Override
        public StepDecision execute(final Context context, final Integer n) {
            if (n <= 1) {
                return StepDecision.gracefulComplete(1);
            }
            final int smallerFactorial =
                    SubFlow.getConditionResults(context).getSingleOutput(Integer.class);
            return StepDecision.gracefulComplete(n * smallerFactorial);
        }
    }
}
