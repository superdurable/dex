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

import java.util.ArrayList;
import java.util.List;

/** WCP-21 Structured Loop: a pre-test Step routes to the loop body or the exit. */
@Component
public class StructuredLoopFlow implements Flow<StructuredLoopFlow.BatchProgress> {
    /** How many batches remain and which batches have been processed. */
    public record BatchProgress(int remainingBatches, List<String> processedBatches) {
    }

    private final CheckBatches checkBatches = new CheckBatches();
    private final ProcessBatch processBatch = new ProcessBatch();
    private final FinishBatches finishBatches = new FinishBatches();

    @Override
    public StepList<BatchProgress> getSteps() {
        return StepList.startStep(checkBatches).otherSteps(processBatch, finishBatches);
    }

    static final class CheckBatches implements Step<BatchProgress> {
        @Override
        public Class<BatchProgress> getInputType() {
            return BatchProgress.class;
        }

        @Override
        public StepDecision execute(final Context context, final BatchProgress progress) {
            if (progress.remainingBatches() == 0) {
                return StepDecision.goTo(
                        FinishBatches.class, progress.processedBatches().toArray(new String[0]));
            }
            return StepDecision.goTo(ProcessBatch.class, progress);
        }
    }

    static final class ProcessBatch implements Step<BatchProgress> {
        @Override
        public Class<BatchProgress> getInputType() {
            return BatchProgress.class;
        }

        @Override
        public StepDecision execute(final Context context, final BatchProgress progress) {
            final List<String> processed = new ArrayList<String>(progress.processedBatches());
            processed.add("batch-" + progress.remainingBatches());
            return StepDecision.goTo(
                    CheckBatches.class, new BatchProgress(progress.remainingBatches() - 1, processed));
        }
    }

    static final class FinishBatches implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] processedBatches) {
            return StepDecision.gracefulComplete(processedBatches);
        }
    }
}
