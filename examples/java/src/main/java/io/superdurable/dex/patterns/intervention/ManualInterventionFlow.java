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

package io.superdurable.dex.patterns.intervention;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

/**
 * Handles API-call failure scenarios with manual retry or skip intervention.
 */
@Component
public class ManualInterventionFlow implements Flow<Void> {
    public static final String INTERNAL_CHANNEL_COMMAND = "internal_channel_command";
    public static final String SIGNAL_CHANNEL_COMMAND_RETRY = "signal_channel_command_retry";
    public static final String SIGNAL_CHANNEL_COMMAND_SKIP = "signal_channel_command_skip";
    public static final String NUMBER_OF_RETRIES = "number_of_retries";

    public final Channel<String> dataChannel =
            Channel.define(INTERNAL_CHANNEL_COMMAND, String.class);
    public final Channel<Void> retrySignal =
            Channel.define(SIGNAL_CHANNEL_COMMAND_RETRY, Void.class);
    public final Channel<Void> skipSignal =
            Channel.define(SIGNAL_CHANNEL_COMMAND_SKIP, Void.class);
    public final Attribute<Integer> numberOfRetries =
            Attribute.define(NUMBER_OF_RETRIES, Integer.class);

    private final Init init = new Init();
    private final GetData getData = new GetData();
    private final Error error = new Error();
    private final Final finalStep = new Final();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(init).otherSteps(getData, error, finalStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(dataChannel, retrySignal, skipSignal, numberOfRetries);
    }

    final class Init implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            numberOfRetries.set(context, 0);
            return StepDecision.goTo(getData, false);
        }
    }

    final class GetData implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public Wait waitFor(final Context context, final Boolean isRetry) {
            System.out.println("Waiting for incoming data");
            return Wait.until(dataChannel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Boolean isRetry) {
            if (Boolean.TRUE.equals(isRetry)) {
                final Integer retries = numberOfRetries.get(context);
                numberOfRetries.set(context, retries + 1);
            }
            try {
                pretendApiCall(context);
            } catch (final Exception e) {
                return StepDecision.goTo(error, null);
            }
            return StepDecision.goTo(finalStep, null);
        }

        private void pretendApiCall(final Context context) {
            final List<String> results = dataChannel.getConditionResults(context);
            if (!results.isEmpty()) {
                final String data = results.get(0);
                System.out.println("Received data result: " + data);
                if ("failed".equals(data)) {
                    throw new IllegalArgumentException("Non-retryable exception");
                }
            }
        }
    }

    final class Error implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(retrySignal.forOne(), skipSignal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final boolean retry =
                    !retrySignal.getConditionResults(context).isEmpty();
            System.out.println("signal received: "
                    + (retry ? SIGNAL_CHANNEL_COMMAND_RETRY : SIGNAL_CHANNEL_COMMAND_SKIP));
            if (retry) {
                return StepDecision.goTo(getData, true);
            }
            return StepDecision.goTo(finalStep, null);
        }
    }

    final class Final implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final Integer retries = numberOfRetries.get(context);
            return StepDecision.gracefulComplete(
                    "Workflow Completed. Number of retries: " + retries);
        }
    }
}
