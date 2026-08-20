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

package io.superdurable.dex.primitives.waittypes;

import io.superdurable.dex.Channel;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

final class WaitTypesInput {
    private final String mode;
    private final int timeoutSeconds;

    public WaitTypesInput(final String mode, final int timeoutSeconds) {
        this.mode = mode;
        this.timeoutSeconds = timeoutSeconds;
    }

    public String getMode() {
        return mode;
    }

    public int getTimeoutSeconds() {
        return timeoutSeconds;
    }
}

@Component
public final class WaitTypesFlow implements Flow<WaitTypesInput> {
    public final Channel<String> channelA = Channel.define("SignalA", String.class);
    public final Channel<String> channelB = Channel.define("SignalB", String.class);
    private final WaitTypesStep waitTypes = new WaitTypesStep();

    @Override
    public StepList<WaitTypesInput> getSteps() {
        return StepList.startStep(waitTypes);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(channelA, channelB);
    }

    @RPC
    public void signalA(final Context context) {
        channelA.publish(context, "signal-a");
    }

    @RPC
    public void signalB(final Context context) {
        channelB.publish(context, "signal-b");
    }

    final class WaitTypesStep implements Step<WaitTypesInput> {
        @Override
        public Class<WaitTypesInput> getInputType() {
            return WaitTypesInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final WaitTypesInput input) {
            final Duration timeout = Duration.ofSeconds(input.getTimeoutSeconds());
            switch (input.getMode()) {
                case "any":
                    return Wait.anyOf(
                            channelA.forOne("signal"),
                            Timer.byDuration(timeout, "timeout"));
                case "all":
                    return Wait.allOf(
                            channelA.forOne("signal-a"),
                            channelB.forOne("signal-b"));
                case "combo":
                    return Wait.anyCombinationOf(
                            ConditionCombination.of(
                                    channelA.forOne("signal-a"),
                                    Timer.byDuration(timeout, "timeout")),
                            ConditionCombination.of(channelB.forOne("signal-b")));
                default:
                    throw new IllegalArgumentException("unknown wait mode: " + input.getMode());
            }
        }

        @Override
        public StepDecision execute(final Context context, final WaitTypesInput input) {
            return StepDecision.gracefulComplete(input.getMode());
        }
    }
}
