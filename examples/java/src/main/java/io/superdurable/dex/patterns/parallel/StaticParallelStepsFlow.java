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

package io.superdurable.dex.patterns.parallel;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import org.springframework.stereotype.Component;

@Component
public class StaticParallelStepsFlow implements Flow<String> {
    private final InitStep init = new InitStep();
    private final WorkA workA = new WorkA();
    private final WorkB workB = new WorkB();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(init).otherSteps(workA, workB);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class InitStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.goToMany(
                    StepMovement.of(WorkA.class, input),
                    StepMovement.of(WorkB.class, input));
        }
    }

    final class WorkA implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete("A:" + input);
        }
    }

    final class WorkB implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete("B:" + input);
        }
    }
}
