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

package io.superdurable.dex.patterns.workflow.drainchannels.signal;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class DrainSignalChannelsFlow implements Flow<String> {
    public static final String QUEUE_SIGNAL_CHANNEL = "queueSignalChannel";

    public final Channel<String> queueSignalChannel =
            Channel.define(QUEUE_SIGNAL_CHANNEL, String.class);

    private final ProcessSignal processSignal = new ProcessSignal();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(processSignal);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(queueSignalChannel);
    }

    final class ProcessSignal implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            if (input == null) {
                return Wait.anyOf(queueSignalChannel.forOne());
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            if (input != null) {
                System.out.println(
                        "DrainSignalChannelsFlow process signal value: " + input);
            } else {
                final List<String> values = queueSignalChannel.getConditionResults(context);
                if (values.isEmpty()) {
                    throw new IllegalStateException("No signal request found");
                }
                final String value = values.get(0);
                if (value == null) {
                    throw new IllegalStateException("No signal value found");
                }
                System.out.println(
                        "DrainSignalChannelsFlow process signal value: " + value);
            }

            try {
                Thread.sleep(20000);
            } catch (final InterruptedException e) {
                throw new RuntimeException(e);
            }

            return StepDecision.forceCompleteWhenChannelsEmpty(
                    null,
                    StepMovement.of(processSignal, null),
                    queueSignalChannel);
        }
    }
}
