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

package io.superdurable.dex.integ;

import io.superdurable.dex.exceptions.ChannelMessageNotFoundException;
import io.superdurable.dex.primitives.channel.ChannelFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

@ExtendWith(SharedIntegExtension.class)
public class ChannelIntegTest {
    @Test
    void channelMessageCanBeMovedById() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final ChannelFlow flow = environment.channelFlow();
        final String flowId = environment.newFlowId("channel-message");
        environment.client().startFlow(flow, flowId, 30, environment.startOptions());
        final ChannelFlow stub = environment.client().newRpcStub(ChannelFlow.class, flowId);
        environment.client().invokeRPC(stub::enqueue, "delete me");
        environment.client().invokeRPC(stub::enqueue, "move me");

        final List<ChannelFlow.PendingMessage> pending =
                environment.client().invokeRPC(stub::getQueuedMessages).messages;
        assertEquals(List.of("delete me", "move me"), pending.stream()
                .map(message -> message.value)
                .toList());
        environment.client().invokeRPC(
                stub::deleteQueued,
                new ChannelFlow.MoveMessage(pending.get(0).messageId));

        final ChannelFlow.MoveMessage move = new ChannelFlow.MoveMessage(pending.get(1).messageId);
        environment.client().invokeRPC(stub::move, move);
        assertEquals(List.of("move me"), environment.client().invokeRPC(stub::getMovedMessages).messages
                .stream()
                .map(message -> message.value)
                .toList());

        assertThrows(
                ChannelMessageNotFoundException.class,
                () -> environment.client().invokeRPC(stub::move, move));
        assertEquals(List.of("move me"), environment.client().invokeRPC(stub::getMovedMessages).messages
                .stream()
                .map(message -> message.value)
                .toList());

        environment.client().invokeRPC(stub::approve);
    }
}
