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

package io.superdurable.dex.products.engagement;

import io.superdurable.dex.Client;
import io.superdurable.dex.SearchFlowsPage;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/products/engagement")
public class EngagementController {
    private final Client client;
    private final EngagementFlow flow;

    public EngagementController(
            final Client client,
            final EngagementFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start() {
        final String flowId = "engagement-" + System.nanoTime();
        final EngagementInput input = new EngagementInput(
                "test-employer-id",
                "test-job-seeker-id",
                "test-notes");
        final String runId = client.startFlow(flow, flowId, input, ExampleFlows.startOptions());
        client.waitForAttributeEqual(
                flowId,
                flow.employerId,
                input.employerId,
                Duration.ofSeconds(15));
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", flowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/describe")
    public ResponseEntity<EngagementDescription> describe(
            @RequestParam final String workflowId) {
        final EngagementFlow stub = client.newRpcStub(EngagementFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::describe));
    }

    @GetMapping("/optout")
    public ResponseEntity<Map<String, Object>> optOut(
            @RequestParam final String workflowId) {
        client.publish(workflowId, flow.optOutReminder, (Void) null);
        return ResponseEntity.ok(Collections.<String, Object>emptyMap());
    }

    @GetMapping("/decline")
    public ResponseEntity<Status> decline(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "") final String notes) {
        final EngagementFlow stub = client.newRpcStub(EngagementFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::decline, notes));
    }

    @GetMapping("/accept")
    public ResponseEntity<Status> accept(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "") final String notes) {
        final EngagementFlow stub = client.newRpcStub(EngagementFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::accept, notes));
    }

    @GetMapping("/list")
    public ResponseEntity<SearchFlowsPage> list(@RequestParam final String query) {
        return ResponseEntity.ok(client.searchFlows(query, 100, ""));
    }
}
