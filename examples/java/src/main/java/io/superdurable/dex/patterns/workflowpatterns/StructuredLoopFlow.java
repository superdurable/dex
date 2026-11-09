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
import org.springframework.stereotype.Component;

/** WCP-21 Structured Loop: a Step repeats itself while a pre-test condition holds. */
@Component
public class StructuredLoopFlow implements Flow<StructuredLoopFlow.BatchProgress> {
    /** How many batches remain and how many have been processed. */
    public record BatchProgress(int remainingBatches, int processedBatches) {
    }

    private final ProcessBatches processBatches = new ProcessBatches();
    private final FinishBatches finishBatches = new FinishBatches();

    @Override
    public StepList<BatchProgress> getSteps() {
        return StepList.startStep(processBatches).otherSteps(finishBatches);
    }

    static final class ProcessBatches implements Step<BatchProgress> {
        @Override
        public Class<BatchProgress> getInputType() {
            return BatchProgress.class;
        }

        @Override
        public StepDecision execute(final Context context, final BatchProgress progress) {
            if (progress.remainingBatches() == 0) {
                return StepDecision.goTo(FinishBatches.class, progress.processedBatches());
            }
            return StepDecision.goTo(
                    ProcessBatches.class,
                    new BatchProgress(
                            progress.remainingBatches() - 1, progress.processedBatches() + 1));
        }
    }

    static final class FinishBatches implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer processedBatches) {
            return StepDecision.gracefulComplete(processedBatches);
        }
    }
}
