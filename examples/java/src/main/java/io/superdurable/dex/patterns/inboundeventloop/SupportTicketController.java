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

package io.superdurable.dex.patterns.inboundeventloop;

import io.superdurable.dex.AttributeMapInstance;
import io.superdurable.dex.Client;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.patterns.inboundeventloop.SupportTicketConversationFlow.TicketReply;
import io.superdurable.dex.patterns.inboundeventloop.SupportTicketConversationFlow.TranscriptChunk;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;
import java.util.Map;

@RestController
@RequestMapping("/patterns/inbound-event-loop")
public class SupportTicketController {
    private final Client client;
    private final SupportTicketConversationFlow supportTicketFlow;

    public SupportTicketController(
            final Client client,
            final SupportTicketConversationFlow supportTicketFlow) {
        this.client = client;
        this.supportTicketFlow = supportTicketFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startSupportTicket(@RequestParam final String workflowId) {
        // No Flow timeout: the 24-hour inactivity Timer closes a quiet ticket.
        final String runId = client.startFlow(
                supportTicketFlow,
                workflowId,
                "",
                StartFlowOptions.newBuilder().build());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/reply")
    ResponseEntity<String> sendTicketReply(
            @RequestParam final String workflowId,
            @RequestParam final String text) {
        final SupportTicketConversationFlow stub =
                client.newRpcStub(SupportTicketConversationFlow.class, workflowId);
        client.invokeRPC(stub::sendTicketReply, text);
        return ResponseEntity.ok("reply accepted");
    }

    @GetMapping("/resolve")
    ResponseEntity<String> resolveTicket(
            @RequestParam final String workflowId,
            @RequestParam final String reason) {
        final SupportTicketConversationFlow stub =
                client.newRpcStub(SupportTicketConversationFlow.class, workflowId);
        client.invokeRPC(stub::resolveTicket, reason);
        return ResponseEntity.ok("resolution accepted");
    }

    @GetMapping("/transcript")
    ResponseEntity<Map<String, List<TicketReply>>> getTicketTranscript(
            @RequestParam final String workflowId) {
        final AttributeMapInstance<TranscriptChunk> currentChunk =
                SupportTicketConversationFlow.transcriptChunks.instance(
                        SupportTicketConversationFlow.CURRENT_TRANSCRIPT_CHUNK);
        final List<TicketReply> replies = client.getAttributes(workflowId, currentChunk)
                .get(currentChunk)
                .map(chunk -> chunk.replies)
                .orElse(List.of());
        return ResponseEntity.ok(Map.of("replies", replies));
    }
}
