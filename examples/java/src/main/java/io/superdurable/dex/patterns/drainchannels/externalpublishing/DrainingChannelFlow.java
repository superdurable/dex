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

package io.superdurable.dex.patterns.drainchannels.externalpublishing;

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

import java.util.List;

@Component
public class DrainingChannelFlow implements Flow<String> {
    public static final String QUEUE_CHANNEL = "queueChannel";

    public final Channel<String> queueChannel =
            Channel.define(QUEUE_CHANNEL, String.class);

    private final ProcessMessage processMessage = new ProcessMessage();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(processMessage);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(queueChannel);
    }

    @RPC
    public RPCResult<String> exampleRPC(final Context context, final String input) {
        queueChannel.publish(context, input);
        return RPCResult.of(input);
    }

    final class ProcessMessage implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            if (input == null) {
                return Wait.until(queueChannel.forOne());
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            if (input != null) {
                System.out.println(
                        "DrainingChannelFlow process message: " + input);
            } else {
                final List<String> values = queueChannel.getConditionResults(context);
                if (values.isEmpty()) {
                    throw new IllegalStateException("No channel message found");
                }
                final String value = values.get(0);
                if (value == null) {
                    throw new IllegalStateException("No channel message value found");
                }
                System.out.println(
                        "DrainingChannelFlow process message: " + value);
            }

            try {
                Thread.sleep(20000);
            } catch (final InterruptedException e) {
                throw new RuntimeException(e);
            }

            return StepDecision.forceCompleteIfChannelsEmpty(
                    null,
                    StepMovement.of(ProcessMessage.class, null),
                    queueChannel);
        }
    }
}
