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

package io.superdurable.dex.integ;

import io.superdurable.dex.workflow.subscription.Customer;
import io.superdurable.dex.workflow.subscription.Subscription;
import io.superdurable.dex.workflow.subscription.SubscriptionFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

@ExtendWith(SharedIntegExtension.class)
public class SubscriptionIntegTest {
    @Test
    void subscriptionStartRpcAndChannels() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final SubscriptionFlow flow = environment.subscriptionFlow();
        final String flowId = environment.newFlowId("subscription");
        final Customer customer = new Customer(
                "Example",
                "Customer",
                flowId,
                "customer@example.com",
                new Subscription(
                        Duration.ofSeconds(30),
                        Duration.ofSeconds(30),
                        2,
                        100));

        final String runId = environment.client().startFlow(
                flow,
                flowId,
                customer,
                environment.startOptions());
        assertNotNull(runId);
        assertFalse(runId.isEmpty());

        environment.awaitCondition(
                () -> environment.client().getAttribute(flowId, flow.customerDetails),
                details -> details != null
                        && flowId.equals(details.id)
                        && details.subscription != null
                        && details.subscription.billingPeriodCharge == 100,
                Duration.ofSeconds(20),
                "customer details not ready");

        final SubscriptionFlow stub =
                environment.client().newRpcStub(SubscriptionFlow.class, flowId);
        final Subscription current = environment.client().invokeRPC(stub::describe);
        assertEquals(100, current.billingPeriodCharge);

        environment.client().publish(flowId, flow.updateChargeAmount, 250);
        environment.awaitCondition(
                () -> environment.client().invokeRPC(stub::describe),
                subscription -> subscription != null
                        && subscription.billingPeriodCharge == 250,
                Duration.ofSeconds(20),
                "Describe charge amount did not update");

        environment.client().publish(flowId, flow.cancelSubscription, (Void) null);

        final String output = environment.client().waitForFlow(flowId, Duration.ofSeconds(45)).getSingleOutput(String.class);
        assertEquals("subscription canceled", output);
    }
}
