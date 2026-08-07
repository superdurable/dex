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

package io.superdurable.dex.controller;

import io.superdurable.dex.Client;
import io.superdurable.dex.workflow.subscription.Customer;
import io.superdurable.dex.workflow.subscription.Subscription;
import io.superdurable.dex.workflow.subscription.SubscriptionFlow;
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
@RequestMapping("/subscription")
public class SubscriptionWorkflowController {
    private final Client client;
    private final SubscriptionFlow flow;

    public SubscriptionWorkflowController(
            final Client client,
            final SubscriptionFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start() {
        final String flowId = "subscription-" + System.nanoTime();
        final Customer customer = new Customer(
                "Quanzheng",
                "Long",
                "qlong",
                "qlong@example.com",
                new Subscription(
                        Duration.ofSeconds(20),
                        Duration.ofSeconds(10),
                        10,
                        100));
        final String runId = client.startFlow(flow, flowId, customer, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", flowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/cancel")
    public ResponseEntity<Map<String, Object>> cancel(
            @RequestParam final String workflowId) {
        client.publish(workflowId, flow.cancelSubscription, (Void) null);
        return ResponseEntity.ok(Collections.<String, Object>emptyMap());
    }

    @GetMapping("/updateChargeAmount")
    public ResponseEntity<Map<String, Object>> updateChargeAmount(
            @RequestParam final String workflowId,
            @RequestParam final int newChargeAmount) {
        client.publish(workflowId, flow.updateChargeAmount, newChargeAmount);
        return ResponseEntity.ok(Collections.<String, Object>emptyMap());
    }

    @GetMapping("/describe")
    public ResponseEntity<Subscription> describe(
            @RequestParam final String workflowId) {
        final SubscriptionFlow stub = client.newRpcStub(SubscriptionFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::describe));
    }
}
