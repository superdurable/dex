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

package io.superdurable.dex.products.microservices;

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
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class OrchestrationFlow implements Flow<String> {
    public final Attribute<String> data = Attribute.define("data", String.class);
    public final Channel<Void> ready = Channel.define("Ready", Void.class);

    private final MyDependencyService service;
    private final CallAPI1 callAPI1 = new CallAPI1();
    private final CallAPI2 callAPI2 = new CallAPI2();
    private final CallAPI3 callAPI3 = new CallAPI3();
    private final CallAPI4 callAPI4 = new CallAPI4();

    public OrchestrationFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(callAPI1).otherSteps(callAPI2, callAPI3, callAPI4);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(data, ready);
    }

    @RPC
    public RPCResult<String> swap(final Context context, final String newData) {
        final String oldData = data.get(context);
        data.set(context, newData);
        return RPCResult.of(oldData);
    }

    final class CallAPI1 implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            service.callAPI1(input);
            data.set(context, input);
            return StepDecision.goToMany(
                    StepMovement.of(CallAPI2.class, null),
                    StepMovement.of(CallAPI3.class, null));
        }
    }

    final class CallAPI2 implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            service.callAPI2(data.get(context));
            return StepDecision.deadEnd();
        }
    }

    final class CallAPI3 implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(Timer.byDuration(Duration.ofHours(24)), ready.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String value = data.get(context);
            service.callAPI3(value);
            if (context.hasTimerFired()) {
                return StepDecision.goTo(CallAPI4.class, null);
            }
            return StepDecision.gracefulComplete(value);
        }
    }

    final class CallAPI4 implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String value = data.get(context);
            service.callAPI4(value);
            return StepDecision.gracefulComplete(value);
        }
    }
}
