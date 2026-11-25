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

package io.superdurable.dex.patterns.drainchannels;

import io.superdurable.dex.Client;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.patterns.drainchannels.internal.DrainInternalChannelFlow;
import io.superdurable.dex.patterns.drainchannels.externalpublishing.DrainingExternalChannelFlow;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.UUID;

@RestController
@RequestMapping("/patterns/drain-channels")
public class DrainChannelsController {
    /** Bounds how often a message that lost a start race is published again. */
    private static final int START_OR_PUBLISH_ATTEMPTS = 3;

    private final Client client;
    private final DrainInternalChannelFlow drainInternalChannelFlow;
    private final DrainingExternalChannelFlow drainingExternalChannelFlow;

    public DrainChannelsController(
            final Client client,
            final DrainInternalChannelFlow drainInternalChannelFlow,
            final DrainingExternalChannelFlow drainingExternalChannelFlow) {
        this.client = client;
        this.drainInternalChannelFlow = drainInternalChannelFlow;
        this.drainingExternalChannelFlow = drainingExternalChannelFlow;
    }

    @GetMapping("/internal/start")
    ResponseEntity<String> startDrainInternalChannels(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                drainInternalChannelFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/external-publishing/start-or-publish")
    ResponseEntity<String> startDrainingChannel(
            @RequestParam final String workflowId,
            @RequestParam(required = false) final String messageId) {
        final String stableMessageId =
                messageId == null || messageId.isEmpty() ? UUID.randomUUID().toString() : messageId;
        final String message = "message " + stableMessageId;
        final DrainingExternalChannelFlow stub = client.newRpcStub(
                DrainingExternalChannelFlow.class,
                workflowId);
        for (int attempt = 0; attempt < START_OR_PUBLISH_ATTEMPTS; attempt++) {
            try {
                final String runId = client.startFlow(
                        drainingExternalChannelFlow,
                        workflowId,
                        message,
                        externalPublishingStartOptions(stableMessageId));
                return ResponseEntity.ok(
                        "Accepted message " + stableMessageId + " as the start input of runId " + runId);
            } catch (final FlowAlreadyStartedException alreadyStarted) {
                // A different start request owns the active run: publish this message to that run.
            }
            try {
                client.invokeRPC(stub::publishExternalChannelMessage, message);
                return ResponseEntity.ok("Accepted message " + stableMessageId);
            } catch (final FlowNotActiveOrNotFoundException inactive) {
                // That run closed before the publish: start again.
            }
        }
        return ResponseEntity.status(HttpStatus.CONFLICT).body(
                "message " + stableMessageId + " is unconfirmed; send it again with the same messageId");
    }

    /**
     * Restarts a closed Flow ID. A retried start returns the original run while that run is
     * still the latest run for the Flow ID and its start request ID matches.
     */
    private static StartFlowOptions externalPublishingStartOptions(final String messageId) {
        return StartFlowOptions.newBuilder()
                .timeout(Duration.ofHours(1))
                .idReusePolicy(IdReusePolicy.ALLOW_IF_NOT_RUNNING)
                .requestId("start:" + messageId)
                .ignoreAlreadyStarted(true)
                .build();
    }
}
