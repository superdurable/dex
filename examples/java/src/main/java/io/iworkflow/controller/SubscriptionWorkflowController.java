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

package io.iworkflow.controller;

import io.iworkflow.core.Client;
import io.iworkflow.core.ImmutableWorkflowOptions;
import io.iworkflow.gen.models.WorkflowConfig;
import io.iworkflow.workflow.subscription.SubscriptionWorkflow;
import io.iworkflow.workflow.subscription.model.Customer;
import io.iworkflow.workflow.subscription.model.ImmutableCustomer;
import io.iworkflow.workflow.subscription.model.ImmutableSubscription;
import io.iworkflow.workflow.subscription.model.Subscription;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

import java.time.Duration;

import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.signalCancelSubscription;
import static io.iworkflow.workflow.subscription.SubscriptionWorkflow.signalUpdateBillingPeriodCharge;

@Controller
@RequestMapping("/subscription")
public class SubscriptionWorkflowController {

    private final Client client;

    public SubscriptionWorkflowController(
            final Client client
    ) {
        this.client = client;
    }

    @GetMapping("/start")
    public ResponseEntity<String> start() {
        final String wfId = "subscription_test_id_" + System.currentTimeMillis() / 1000;
        final Customer customer = ImmutableCustomer.builder()
                .firstName("Quanzheng")
                .lastName("Long")
                .id("qlong")
                .email("qlong.seattle@gmail.com")
                .subscription(
                        ImmutableSubscription.builder()
                                .trialPeriod(Duration.ofSeconds(20))
                                .billingPeriod(Duration.ofSeconds(10))
                                .maxBillingPeriods(0) // support unlimited periods because of auto continueAsNew in IWF
                                .billingPeriodCharge(100)
                                .build()
                )
                .build();
        final String runId = client.startWorkflow(SubscriptionWorkflow.class, wfId, 3600, customer,
                ImmutableWorkflowOptions.builder()
                        .workflowConfigOverride(
                                // set lower threshold to demo auto continueAsNew
                                new WorkflowConfig().continueAsNewThreshold(10)
                        )
                        .build());

        return ResponseEntity.ok(String.format("workflowId: %s: runId: %s", wfId, runId));
    }

    @GetMapping("/cancel")
    public ResponseEntity<String> cancel(
            @RequestParam String workflowId
    ) {
        client.signalWorkflow(SubscriptionWorkflow.class, workflowId, signalCancelSubscription, null);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/describe")
    public ResponseEntity<Subscription> describe(
            @RequestParam String workflowId
    ) {
        final SubscriptionWorkflow rpcStub = client.newRpcStub(SubscriptionWorkflow.class, workflowId);
        final Subscription subscription = client.invokeRPC(rpcStub::describe);

        return ResponseEntity.ok(subscription);
    }

    @GetMapping("/updateChargeAmount")
    public ResponseEntity<String> updateChargeAmount(
            @RequestParam String workflowId,
            @RequestParam int newChargeAmount
    ) {
        client.signalWorkflow(SubscriptionWorkflow.class, workflowId, signalUpdateBillingPeriodCharge, newChargeAmount);
        return ResponseEntity.ok("done");
    }
}