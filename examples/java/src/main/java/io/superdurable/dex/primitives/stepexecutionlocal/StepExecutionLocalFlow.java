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

package io.superdurable.dex.primitives.stepexecutionlocal;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

@Component
public final class StepExecutionLocalFlow implements Flow<Integer> {
    private final Channel<String> approval = Channel.define("Approval", String.class);
    private final NoteWaitStep noteWait = new NoteWaitStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(noteWait);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(approval);
    }

    final class NoteWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            context.setStepExecutionLocal("note", "approval:" + input, String.class);
            return Wait.until(approval.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final String note = context.getStepExecutionLocal("note", String.class);
            return StepDecision.gracefulComplete(note == null ? "" : note);
        }
    }
}
