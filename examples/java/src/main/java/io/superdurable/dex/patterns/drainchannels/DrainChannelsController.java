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
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.patterns.drainchannels.internal.DrainInternalChannelsFlow;
import io.superdurable.dex.patterns.drainchannels.externalpublishing.DrainingExternalChannelFlow;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/drain-channels")
public class DrainChannelsController {
    private final Client client;
    private final DrainInternalChannelsFlow drainInternalChannelsFlow;
    private final DrainingExternalChannelFlow drainingExternalChannelFlow;

    public DrainChannelsController(
            final Client client,
            final DrainInternalChannelsFlow drainInternalChannelsFlow,
            final DrainingExternalChannelFlow drainingExternalChannelFlow) {
        this.client = client;
        this.drainInternalChannelsFlow = drainInternalChannelsFlow;
        this.drainingExternalChannelFlow = drainingExternalChannelFlow;
    }

    @GetMapping("/internal/start")
    ResponseEntity<String> startDrainInternalChannels(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                drainInternalChannelsFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/external-publishing/start-or-publish")
    ResponseEntity<String> startDrainingChannel(@RequestParam final String workflowId) {
        String response;
        try {
            client.publish(
                    workflowId,
                    drainingExternalChannelFlow.queueChannel,
                    "message from start-or-publish endpoint");
            response = "Published to the Flow";
        } catch (final FlowNotActiveException inactive) {
            final String runId = client.startFlow(
                    drainingExternalChannelFlow,
                    workflowId,
                    "first message from start-or-publish",
                    ExampleFlows.startOptions());
            response = "Started the workflow with runId " + runId;
        }
        return ResponseEntity.ok(response);
    }
}
