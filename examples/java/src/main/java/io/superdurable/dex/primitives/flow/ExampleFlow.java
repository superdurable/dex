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

package io.superdurable.dex.primitives.flow;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

@Component
public class ExampleFlow implements Flow<Integer> {
    public static final Attribute<String> status = Attribute.define("status", String.class);
    public static final Channel<Void> notify = Channel.define("notify", Void.class);

    private final ExampleStep exampleStep = new ExampleStep();
    private final FinishStep finishStep = new FinishStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(exampleStep).otherSteps(finishStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(status, notify);
    }

    @Override
    public StepDecision handleTimeout(final Context context) {
        status.set(context, "timed out");
        return StepDecision.forceFail("processing deadline reached");
    }

    @RPC
    public RPCResult<String> describe(final Context context) {
        return RPCResult.of(status.get(context));
    }

    final class ExampleStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            status.set(context, "running");
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goTo(finishStep, input + 1);
        }
    }

    static final class FinishStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            status.set(context, "done");
            return StepDecision.gracefulComplete(input + 1);
        }
    }
}
