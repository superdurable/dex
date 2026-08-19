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

package io.superdurable.dex.products.orderprocessing;

import io.superdurable.dex.Client;
import io.superdurable.dex.StepExecutionId;
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
@RequestMapping("/products/order-processing")
public class OrderProcessingController {
    private final Client client;
    private final OrderProcessingFlow flow;

    public OrderProcessingController(
            final Client client,
            final OrderProcessingFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam(defaultValue = "false") final boolean testFailAtShipping) {
        final String flowId = "order-processing-" + System.nanoTime();
        final OrderRequest request = new OrderRequest(
                flowId,
                "buyer@example.com",
                "customer-1",
                42,
                testFailAtShipping);
        final String runId = client.startFlow(flow, flowId, request, ExampleFlows.startOptions());
        client.waitForStepCompletion(
                flowId,
                StepExecutionId.of("ChargeStep"),
                Duration.ofMinutes(5));
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", flowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/approve")
    public ResponseEntity<String> approve(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "") final String notes) {
        final OrderProcessingFlow stub = client.newRpcStub(OrderProcessingFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::approve, notes));
    }

    @GetMapping("/describe")
    public ResponseEntity<Map<String, String>> describe(
            @RequestParam final String workflowId) {
        final OrderProcessingFlow stub = client.newRpcStub(OrderProcessingFlow.class, workflowId);
        final String status = client.invokeRPC(stub::describe);
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("status", status);
        return ResponseEntity.ok(response);
    }
}
