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

package io.superdurable.dex.primitives.rpc;

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
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

@Component
public class RpcFlow implements Flow<Integer> {
    public final Channel<Void> exampleCh = Channel.define("rpc-internal", Void.class);
    public final Attribute<String> data = Attribute.define("rpc-data", String.class);
    private final RpcCompleteStep complete = new RpcCompleteStep();
    private final ExampleStep exampleStep = new ExampleStep();
    private final RpcWaitStep wait = new RpcWaitStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(wait).otherSteps(complete, exampleStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(data, exampleCh);
    }

    @RPC
    public RPCResult<String> trigger(final Context context, final String input) {
        data.set(context, input);
        exampleCh.publish(context, null);
        return RPCResult.of(input, StepMovement.of(exampleStep, input));
    }

    final class RpcWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.until(exampleCh.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goTo(complete, 0);
        }
    }

    static final class RpcCompleteStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input + 1);
        }
    }

    static final class ExampleStep implements Step<String> {
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
