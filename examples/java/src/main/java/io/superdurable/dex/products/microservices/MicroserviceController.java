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

package io.superdurable.dex.products.microservices;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/products/microservices")
public class MicroserviceController {
    private final Client client;
    private final OrchestrationFlow flow;

    public MicroserviceController(
            final Client client,
            final OrchestrationFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId) {
        final String runId = client.startFlow(
                flow,
                workflowId,
                "test initial data",
                ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/swap")
    public ResponseEntity<String> swap(
            @RequestParam final String workflowId,
            @RequestParam final String data) {
        final OrchestrationFlow stub = client.newRpcStub(OrchestrationFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::swap, data));
    }

    @GetMapping("/signal")
    public ResponseEntity<Map<String, Object>> signal(
            @RequestParam final String workflowId) {
        client.publish(workflowId, flow.ready, (Void) null);
        return ResponseEntity.ok(Collections.<String, Object>emptyMap());
    }
}
