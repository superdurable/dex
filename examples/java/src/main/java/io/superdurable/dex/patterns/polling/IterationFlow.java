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
import org.springframework.stereotype.Component;

@Component
public class IterationFlow implements Flow<String> {
    private final IterationStep iterationStep = new IterationStep();

    @Override
    public StepList<String> getSteps() { return StepList.startStep(iterationStep); }

    @Override
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(); }

    final class IterationStep implements Step<String> {
        @Override public Class<String> getInputType() { return String.class; }
        @Override public String getStepType() { return "IterationStep"; }

        @Override
        public StepDecision execute(final Context context, final String pageToken) {
            final String nextPageToken = pageToken.isEmpty() ? "page-2" : pageToken.equals("page-2") ? "page-3" : "";
            System.out.printf("Migrating page %s%n", pageToken);
            return nextPageToken.isEmpty()
                    ? StepDecision.gracefulComplete()
                    : StepDecision.goTo(IterationStep.class, nextPageToken);
        }
    }
}
