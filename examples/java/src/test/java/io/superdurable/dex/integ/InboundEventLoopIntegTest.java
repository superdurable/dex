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

package io.superdurable.dex.integ;

import io.superdurable.dex.AttributeMapInstance;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.TimerId;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.patterns.inboundeventloop.SupportTicketFlow;
import io.superdurable.dex.patterns.inboundeventloop.SupportTicketFlow.TranscriptChunk;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.List;
import java.util.function.BooleanSupplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Asserts the Inbound Event Loop pattern example. */
@ExtendWith(SharedIntegExtension.class)
public class InboundEventLoopIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    @Test
    void recordsRepliesBeforeTheResolution() {
        final String flowId = startSupportTicket("inbound-event-loop");
        final SupportTicketFlow stub = ticketStub(flowId);

        final WorkerInvocationException blankReply = assertThrows(
                WorkerInvocationException.class,
                () -> client().invokeRPC(stub::sendTicketReply, "  "));
        assertTrue(blankReply.getMessage().contains("reply text is required"), blankReply.getMessage());
        final WorkerInvocationException blankReason = assertThrows(
                WorkerInvocationException.class,
                () -> client().invokeRPC(stub::resolveTicket, " "));
        assertTrue(
                blankReason.getMessage().contains("resolution reason is required"),
                blankReason.getMessage());

        client().invokeRPC(stub::sendTicketReply, "printer is offline");
        client().invokeRPC(stub::sendTicketReply, "restarted it, still offline");
        client().invokeRPC(stub::resolveTicket, "replaced the network cable");

        assertEquals("resolved: replaced the network cable", completedOutput(flowId));
        assertEquals(
                List.of("printer is offline", "restarted it, still offline"),
                transcriptTexts(flowId));
    }

    @Test
    void closesAQuietTicket() {
        final String flowId = startSupportTicket("inbound-event-loop-inactive");
        client().invokeRPC(ticketStub(flowId)::sendTicketReply, "is anyone there?");
        awaitCondition(
                () -> transcriptTexts(flowId).equals(List.of("is anyone there?")),
                flowId + " reply was never recorded");
        // The reply ended execution 1, so execution 2 holds the inactivity Timer.
        awaitCondition(() -> {
            try {
                client().skipTimer(
                        flowId,
                        StepExecutionId.of("AwaitTicketMessage", 2),
                        TimerId.byConditionIndex(0));
                return true;
            } catch (final DexServiceException notReady) {
                return false;
            }
        }, flowId + " inactivity Timer was never skipped");
        assertEquals("closed after inactivity", completedOutput(flowId));
    }

    private static String startSupportTicket(final String prefix) {
        final String flowId = environment().newFlowId("pattern-" + prefix);
        client().startFlow(
                environment().workflowPatternFlow(SupportTicketFlow.class),
                flowId,
                "",
                environment().startOptions());
        return flowId;
    }

    private static String completedOutput(final String flowId) {
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), flowId + ": " + result.getErrorMessage());
        final List<String> outputs = result.getCompletions().stream()
                .map((StepCompletion completion) -> completion.getOutput(String.class))
                .toList();
        assertEquals(1, outputs.size(), outputs.toString());
        return outputs.get(0);
    }

    private static List<String> transcriptTexts(final String flowId) {
        final AttributeMapInstance<TranscriptChunk> currentChunk =
                SupportTicketFlow.transcriptChunks.instance(
                        SupportTicketFlow.CURRENT_TRANSCRIPT_CHUNK);
        return client().getAttributes(flowId, currentChunk)
                .get(currentChunk)
                .map(chunk -> chunk.replies.stream().map(reply -> reply.text).toList())
                .orElse(List.of());
    }

    private static SupportTicketFlow ticketStub(final String flowId) {
        return client().newRpcStub(SupportTicketFlow.class, flowId);
    }

    private static IntegEnvironment environment() {
        return SharedIntegExtension.environment();
    }

    private static Client client() {
        return environment().client();
    }

    private static void awaitCondition(final BooleanSupplier condition, final String message) {
        try {
            environment().awaitCondition(
                    condition::getAsBoolean, Boolean::booleanValue, WAIT_TIMEOUT, message);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted waiting: " + message, interrupted);
        }
    }
}
