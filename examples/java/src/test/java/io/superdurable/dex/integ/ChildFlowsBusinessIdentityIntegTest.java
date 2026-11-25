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

import io.superdurable.dex.AttributeMapInstance;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.patterns.childflows.OrderFulfillmentFlow;
import io.superdurable.dex.patterns.childflows.OrderFulfillmentFlow.Order;
import io.superdurable.dex.patterns.childflows.Shipment;
import io.superdurable.dex.patterns.childflows.ShipmentFlow;
import io.superdurable.dex.patterns.childflows.ShipmentRecord;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;

/** Asserts the Child Flows with Business Identity pattern example. */
@ExtendWith(SharedIntegExtension.class)
public class ChildFlowsBusinessIdentityIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    @Test
    void recordsReportedAndCanceledShipments() {
        final String orderId = environment().newFlowId("order");
        final String delivered = orderId + "-1";
        final String canceled = orderId + "-2";
        startOrder(orderId, new Order(
                List.of(
                        new Shipment(delivered, "Seattle", 1),
                        new Shipment(canceled, "Portland", 3600)),
                2));
        waitForShipmentStatus(canceled, FlowStatus.RUNNING);
        client().stopFlow(
                canceled, new StopFlowOptions(StopType.CANCEL, "customer canceled the parcel"));

        final FlowResult result = client().waitForFlow(orderId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        assertEquals("delivered to Seattle", readShipmentRecord(orderId, delivered).outcome());
        assertEquals(new ShipmentRecord("canceled", null), readShipmentRecord(orderId, canceled));

        waitForShipmentStatus(delivered, FlowStatus.COMPLETED);
        assertEquals(
                "delivered to Seattle",
                client().getAttributes(delivered, ShipmentFlow.shipmentOutcome)
                        .get(ShipmentFlow.shipmentOutcome)
                        .orElseThrow(() -> new AssertionError("shipmentOutcome was not set")));
    }

    @Test
    void rejectsAShipmentNumberOwnedByAnotherOrder() {
        final String firstOrderId = environment().newFlowId("order");
        final String shared = firstOrderId + "-1";
        final Order order = new Order(List.of(new Shipment(shared, "Boise", 3600)), 3600);
        startOrder(firstOrderId, order);
        waitForShipmentStatus(shared, FlowStatus.RUNNING);

        final String secondOrderId = environment().newFlowId("order");
        startOrder(secondOrderId, order);
        final FlowResult result = client().waitForFlow(secondOrderId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        assertEquals(
                new ShipmentRecord(OrderFulfillmentFlow.SHIPMENT_REJECTED, null),
                readShipmentRecord(secondOrderId, shared));

        for (final String flowId : List.of(shared, firstOrderId)) {
            client().stopFlow(flowId, new StopFlowOptions(StopType.TERMINATE, ""));
        }
    }

    private static void startOrder(final String orderId, final Order order) {
        client().startFlow(
                environment().workflowPatternFlow(OrderFulfillmentFlow.class),
                orderId,
                order,
                environment().startOptions());
    }

    private static void waitForShipmentStatus(final String shipmentNumber, final FlowStatus status) {
        try {
            environment().awaitCondition(
                    () -> {
                        try {
                            return client().describeFlow(shipmentNumber).getStatus() == status;
                        } catch (final FlowNotFoundException notStartedYet) {
                            return false;
                        }
                    },
                    Boolean::booleanValue,
                    WAIT_TIMEOUT,
                    "shipment " + shipmentNumber + " never reached " + status);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new AssertionError(interrupted);
        }
    }

    private static ShipmentRecord readShipmentRecord(
            final String orderId, final String shipmentNumber) {
        final AttributeMapInstance<ShipmentRecord> instance =
                OrderFulfillmentFlow.shipmentRecords.instance(shipmentNumber);
        return client().getAttributes(orderId, instance)
                .get(instance)
                .orElseThrow(() -> new AssertionError("record of " + shipmentNumber));
    }

    private static IntegEnvironment environment() {
        return SharedIntegExtension.environment();
    }

    private static Client client() {
        return environment().client();
    }
}
