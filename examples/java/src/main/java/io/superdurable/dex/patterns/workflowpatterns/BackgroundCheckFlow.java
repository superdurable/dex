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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** The child Flow of CancelCaseFlow: it parks until a background check completes. */
@Component
public class BackgroundCheckFlow implements Flow<Void> {
    public static final Channel<String> backgroundCheckCompleted =
            Channel.define("backgroundCheckCompleted", String.class);

    private final AwaitBackgroundCheck awaitBackgroundCheck = new AwaitBackgroundCheck();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(awaitBackgroundCheck);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(backgroundCheckCompleted);
    }

    static final class AwaitBackgroundCheck implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(backgroundCheckCompleted.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }
}
