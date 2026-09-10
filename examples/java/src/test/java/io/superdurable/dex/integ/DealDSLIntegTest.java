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

import io.superdurable.dex.products.dealdsl.DealDSLFlow;
import io.superdurable.dex.products.dealdsl.DealStart;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;

@ExtendWith(SharedIntegExtension.class)
public class DealDSLIntegTest {
    @Test
    void dealDSLCompletesAnItemPurchase() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final DealDSLFlow flow = environment.dealDSLFlow();
        final String flowId = environment.newFlowId("deal-dsl");
        environment.client().startFlow(
                flow,
                flowId,
                DealStart.example("buyer-1"),
                environment.startOptions());
        environment.client().waitForAttributeEqual(
                flowId,
                flow.currentState,
                "negotiating",
                Duration.ofSeconds(30));
        environment.client().publish(
                flowId,
                flow.conditionMessages,
                "buyer-decision",
                Map.of("accepted", "true"));
        final Map result = environment.client()
                .waitForFlow(flowId, Duration.ofSeconds(30))
                .getSingleOutput(Map.class);
        assertEquals("deliverItemToBuyer", result.get("lastAction"));
        assertEquals("delivered", result.get("itemDeliveryStatus"));
    }
}
