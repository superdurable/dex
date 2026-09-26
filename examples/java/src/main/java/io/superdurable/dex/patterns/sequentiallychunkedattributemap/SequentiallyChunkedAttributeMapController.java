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

package io.superdurable.dex.patterns.sequentiallychunkedattributemap;

import io.superdurable.dex.Client;
import io.superdurable.dex.RPCInvokeOptions;
import io.superdurable.dex.StartFlowOptions;
import java.util.ArrayList;
import java.util.Map;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/sequentially-chunked-attribute-map")
public class SequentiallyChunkedAttributeMapController {
    private final Client client;
    private final ChunkedSubscriberFlow flow;

    public SequentiallyChunkedAttributeMapController(
            final Client client,
            final ChunkedSubscriberFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @PostMapping("/start")
    ResponseEntity<Map<String, String>> start() {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .ignoreAlreadyStarted(true)
                .addAttribute(
                        flow.archiveState,
                        new ChunkedSubscriberFlow.SubscriberArchiveState(1, ""))
                .addAttribute(
                        flow.subscriberChunks,
                        ChunkedSubscriberFlow.CURRENT_CHUNK_INSTANCE,
                        new ChunkedSubscriberFlow.SubscriberChunk(
                                new ArrayList<ChunkedSubscriberFlow.Subscriber>()))
                .build();
        final String runId = client.startFlow(
                flow,
                ChunkedSubscriberFlow.FLOW_ID,
                null,
                options);
        return ResponseEntity.ok(Map.of(
                "flowID", ChunkedSubscriberFlow.FLOW_ID,
                "runID", runId));
    }

    @PostMapping("/register")
    ResponseEntity<ChunkedSubscriberFlow.Subscriber> registerSubscriber(
            @RequestBody final ChunkedSubscriberFlow.RegisterSubscriberInput input) {
        final ChunkedSubscriberFlow stub = client.newRpcStub(
                ChunkedSubscriberFlow.class,
                ChunkedSubscriberFlow.FLOW_ID);
        return ResponseEntity.ok(client.invokeRPC(stub::registerSubscriber, input));
    }

    @GetMapping("/subscribers")
    ResponseEntity<ChunkedSubscriberFlow.SubscriberPage> getSubscriberPage(
            @RequestParam(defaultValue = "") final String pageToken) {
        final String validatedToken = ChunkedSubscriberFlow.validateSubscriberPageToken(pageToken);
        final RPCInvokeOptions options = RPCInvokeOptions.newBuilder()
                .addLoadAttributeMapInstance(flow.subscriberChunks, validatedToken)
                .build();
        final ChunkedSubscriberFlow stub = client.newRpcStub(
                ChunkedSubscriberFlow.class,
                ChunkedSubscriberFlow.FLOW_ID);
        return ResponseEntity.ok(client.invokeRPC(
                stub::getSubscriberPage,
                new ChunkedSubscriberFlow.GetSubscriberPageInput(validatedToken),
                options));
    }
}
