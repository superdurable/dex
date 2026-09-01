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

package io.superdurable.dex.primitives.stream;

import io.superdurable.dex.Client;
import io.superdurable.dex.StreamMessage;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/primitives/stream")
public final class StreamController {
    private final Client client;
    private final StreamFlow flow;

    public StreamController(final Client client, final StreamFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId,
            @RequestParam final String input) {
        final String runId =
                client.startFlow(flow, workflowId, input, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/write")
    public ResponseEntity<String> write(
            @RequestParam final String workflowId,
            @RequestParam final String source,
            @RequestParam final String message) {
        client.writeStream(workflowId, flow.progress, source, message);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/read")
    public ResponseEntity<Map<String, String>> read(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "") final String resumeToken) {
        final StreamMessage<String> message = client.readStream(
                workflowId,
                flow.progress,
                resumeToken,
                Duration.ofSeconds(20));
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("value", message.getValue());
        response.put("resumeToken", message.getResumeToken());
        response.put("createdTime", message.getCreatedTime().toString());
        response.put("source", message.getSource());
        return ResponseEntity.ok(response);
    }
}
