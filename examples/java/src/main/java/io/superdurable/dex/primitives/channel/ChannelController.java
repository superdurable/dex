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

package io.superdurable.dex.primitives.channel;

import io.superdurable.dex.Client;
import io.superdurable.dex.ChannelMessage;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

@RestController
@RequestMapping("/primitives/channel")
public final class ChannelController {
    private final Client client;
    private final ChannelFlow flow;

    public ChannelController(final Client client, final ChannelFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId,
            @RequestParam final int inputNum) {
        final String runId =
                client.startFlow(flow, workflowId, inputNum, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/approve")
    public ResponseEntity<String> approve(@RequestParam final String workflowId) {
        final ChannelFlow stub = client.newRpcStub(ChannelFlow.class, workflowId);
        client.invokeRPC(stub::approve);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/enqueue")
    public ResponseEntity<String> enqueue(
            @RequestParam final String workflowId,
            @RequestParam final String value) {
        client.publish(workflowId, flow.queued, value);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/messages")
    public ResponseEntity<List<ChannelMessage<String>>> messages(
            @RequestParam final String workflowId) {
        return ResponseEntity.ok(client.getChannelMessages(workflowId, flow.queued));
    }

    @GetMapping("/delete")
    public ResponseEntity<String> delete(
            @RequestParam final String workflowId,
            @RequestParam final String messageId) {
        client.deleteChannelMessage(workflowId, flow.queued, messageId);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/move")
    public ResponseEntity<String> move(
            @RequestParam final String workflowId,
            @RequestParam final String messageId) {
        final ChannelFlow stub = client.newRpcStub(ChannelFlow.class, workflowId);
        client.invokeRPC(stub::move, new ChannelFlow.MoveMessage(messageId));
        return ResponseEntity.ok("done");
    }
}
