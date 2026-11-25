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
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.patterns.childflowsbusinessidentity.OrderFulfillmentFlow;
import io.superdurable.dex.patterns.childflowsbusinessidentity.OrderFulfillmentFlow.Order;
import io.superdurable.dex.patterns.childflowsbusinessidentity.Shipment;
import io.superdurable.dex.patterns.childflowsbusinessidentity.ShipmentFlow;
import io.superdurable.dex.patterns.childflowsbusinessidentity.ShipmentFlow.ShipmentRequest;
import io.superdurable.dex.patterns.childflowsbusinessidentity.ShipmentRecord;
import io.superdurable.dex.patterns.childflowsbusinessidentity.ShipmentReport;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.List;
import java.util.Optional;
import java.util.function.Supplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Asserts the Child Flows with Business Identity pattern example. */
@ExtendWith(SharedIntegExtension.class)
public class ChildFlowsBusinessIdentityIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    private static final long LOCK_CONFLICT_RETRY_MILLIS = 100L;

    @Test
    void recordsTheReportBeforeTheShipmentCloses() {
        final String orderId = environment().newFlowId("order");
        final String delivered = orderId + "-1";
        startOrder(orderId, new Order(List.of(new Shipment(delivered, "Seattle", 1)), 3600));
        final String runId = waitForClaimedRun(orderId, delivered);
        // The reconcile Timer waits an hour, so only the report can fill the outcome.
        waitForShipmentRecord(orderId, delivered, new ShipmentRecord(
                OrderFulfillmentFlow.SHIPMENT_STARTED, runId, "delivered to Seattle"));
        assertEquals(FlowStatus.RUNNING, client().describeFlow(orderId).getStatus());
        terminateFlows(orderId);
    }

    @Test
    void acceptsReportsOnlyFromTheClaimedRun() {
        final String orderId = environment().newFlowId("order");
        final String delivered = orderId + "-1";
        final String canceled = orderId + "-2";
        startOrder(orderId, new Order(
                List.of(
                        new Shipment(delivered, "Seattle", 1),
                        new Shipment(canceled, "Portland", 3600)),
                2));
        final String deliveredRunId = waitForClaimedRun(orderId, delivered);
        final String canceledRunId = waitForClaimedRun(orderId, canceled);
        final ShipmentRecord expectedDelivered =
                new ShipmentRecord("completed", deliveredRunId, "delivered to Seattle");
        waitForShipmentRecord(orderId, delivered, expectedDelivered);

        // A repeated report, a report from another run, and an unknown number change nothing.
        final OrderFulfillmentFlow stub = client().newRpcStub(OrderFulfillmentFlow.class, orderId);
        for (final ShipmentReport report : List.of(
                new ShipmentReport(delivered, deliveredRunId, "delivered to Boise"),
                new ShipmentReport(canceled, "another-run", "delivered to Boise"),
                new ShipmentReport(orderId + "-9", "another-run", "delivered to Boise"))) {
            retryOnLockConflict(() -> client().invokeRPC(stub::recordShipmentOutcome, report));
        }
        assertEquals(expectedDelivered, readShipmentRecord(orderId, delivered));
        assertEquals(
                new ShipmentRecord(OrderFulfillmentFlow.SHIPMENT_STARTED, canceledRunId, null),
                readShipmentRecord(orderId, canceled));
        assertTrue(findShipmentRecord(orderId, orderId + "-9").isEmpty(), "record of -9");

        client().stopFlow(
                canceled, new StopFlowOptions(StopType.CANCEL, "customer canceled the parcel"));
        final FlowResult result = client().waitForFlow(orderId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        assertEquals(new ShipmentRecord("canceled", canceledRunId, null),
                readShipmentRecord(orderId, canceled));
    }

    @Test
    void rejectsAShipmentNumberOwnedByAnotherRequest() {
        final String firstOrderId = environment().newFlowId("order");
        final Shipment shipment = new Shipment(firstOrderId + "-1", "Boise", 3600);
        startOrder(firstOrderId, new Order(List.of(shipment), 3600));
        final String runId = waitForClaimedRun(firstOrderId, shipment.shipmentNumber());

        // Replaying the same start request returns the claimed run.
        final String replayedRunId = client().startFlow(
                environment().workflowPatternFlow(ShipmentFlow.class),
                shipment.shipmentNumber(),
                new ShipmentRequest(firstOrderId, shipment),
                StartFlowOptions.newBuilder()
                        .timeout(OrderFulfillmentFlow.SHIPMENT_TIMEOUT)
                        .idReusePolicy(IdReusePolicy.DISALLOW)
                        .requestId(OrderFulfillmentFlow.shipmentStartRequestId(firstOrderId, shipment))
                        .ignoreAlreadyStarted(true)
                        .build());
        assertEquals(runId, replayedRunId);

        // Another order with the same shipment number sends a different request.
        final String secondOrderId = environment().newFlowId("order");
        startOrder(secondOrderId, new Order(
                List.of(shipment, new Shipment(secondOrderId + "-2", "Austin", 3600)),
                3600));
        final ShipmentRecord rejected =
                new ShipmentRecord(OrderFulfillmentFlow.SHIPMENT_REJECTED, null, null);
        waitForShipmentRecord(secondOrderId, shipment.shipmentNumber(), rejected);
        // The run that owns the number reports to the second order: the rejected record stays as it is.
        final OrderFulfillmentFlow stub =
                client().newRpcStub(OrderFulfillmentFlow.class, secondOrderId);
        retryOnLockConflict(() -> client().invokeRPC(stub::recordShipmentOutcome,
                new ShipmentReport(shipment.shipmentNumber(), runId, "delivered to Boise")));
        assertEquals(rejected, readShipmentRecord(secondOrderId, shipment.shipmentNumber()));
        terminateFlows(
                shipment.shipmentNumber(), firstOrderId, secondOrderId + "-2", secondOrderId);
    }

    @Test
    void ignoresAReopenedShipmentNumber() {
        final String orderId = environment().newFlowId("order");
        final String number = orderId + "-1";
        startOrder(orderId, new Order(List.of(new Shipment(number, "Seattle", 3600)), 3));
        final String claimedRunId = waitForClaimedRun(orderId, number);
        client().stopFlow(number, new StopFlowOptions(StopType.CANCEL, ""));
        waitForShipmentStatus(number, FlowStatus.CANCELED);

        // Another caller reopens the number; its run reports to the same order.
        client().startFlow(
                environment().workflowPatternFlow(ShipmentFlow.class),
                number,
                new ShipmentRequest(orderId, new Shipment(number, "Portland", 1)),
                StartFlowOptions.newBuilder()
                        .idReusePolicy(IdReusePolicy.ALLOW_IF_NOT_RUNNING)
                        .build());
        waitForShipmentStatus(number, FlowStatus.COMPLETED);

        final FlowResult result = client().waitForFlow(orderId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        final ShipmentRecord record = readShipmentRecord(orderId, number);
        assertEquals(claimedRunId, record.runId());
        assertNull(record.outcome(), "the reopened run's outcome must not reach the order");
        assertTrue(
                List.of("canceled", OrderFulfillmentFlow.SHIPMENT_UNAVAILABLE)
                        .contains(record.status()),
                record.status());
    }

    @Test
    void failsAnOrderWithADuplicateShipmentNumber() {
        final String orderId = environment().newFlowId("order");
        final String number = orderId + "-1";
        startOrder(orderId, new Order(
                List.of(
                        new Shipment(number, "Seattle", 1),
                        new Shipment(number, "Portland", 1)),
                2));
        final FlowResult result = client().waitForFlow(orderId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.FAILED, result.getStatus());
        assertTrue(
                result.getErrorMessage().contains("duplicate shipment number: " + number),
                result.getErrorMessage());
        assertThrows(FlowNotFoundException.class, () -> client().describeFlow(number));
    }

    @Test
    void storesTheOutcomeBeforeReportingUnderAsyncDefaults() {
        final String orderId = environment().newFlowId("order");
        final String number = orderId + "-1";
        startOrder(orderId, new Order(List.of(new Shipment(number, "Austin", 3)), 3600));
        final String runId = waitForClaimedRun(orderId, number);
        client().updateFlowConfig(
                number, FlowConfig.newBuilder().stepDurability(StepDurability.ASYNC).build());
        waitForShipmentRecord(orderId, number, new ShipmentRecord(
                OrderFulfillmentFlow.SHIPMENT_STARTED, runId, "delivered to Austin"));
        assertEquals(
                "delivered to Austin",
                client().getAttributes(number, ShipmentFlow.shipmentOutcome)
                        .get(ShipmentFlow.shipmentOutcome)
                        .orElseThrow(() -> new AssertionError("shipmentOutcome was not set")));
        terminateFlows(orderId);
    }

    private static void startOrder(final String orderId, final Order order) {
        client().startFlow(
                environment().workflowPatternFlow(OrderFulfillmentFlow.class),
                orderId,
                order,
                environment().startOptions());
    }

    private static void waitForShipmentStatus(final String shipmentNumber, final FlowStatus status) {
        await(
                () -> {
                    try {
                        return client().describeFlow(shipmentNumber).getStatus() == status;
                    } catch (final FlowNotFoundException notStartedYet) {
                        return false;
                    }
                },
                "shipment " + shipmentNumber + " never reached " + status);
    }

    /** Waits until the order claimed a run of the shipment and returns its run ID. */
    private static String waitForClaimedRun(final String orderId, final String shipmentNumber) {
        await(
                () -> findShipmentRecord(orderId, shipmentNumber)
                        .map(record -> record.runId() != null)
                        .orElse(false),
                "claimed run of " + shipmentNumber);
        return readShipmentRecord(orderId, shipmentNumber).runId();
    }

    private static void waitForShipmentRecord(
            final String orderId, final String shipmentNumber, final ShipmentRecord expected) {
        await(
                () -> findShipmentRecord(orderId, shipmentNumber)
                        .map(expected::equals)
                        .orElse(false),
                "record of " + shipmentNumber + " never became " + expected);
    }

    private static ShipmentRecord readShipmentRecord(
            final String orderId, final String shipmentNumber) {
        return findShipmentRecord(orderId, shipmentNumber)
                .orElseThrow(() -> new AssertionError("record of " + shipmentNumber));
    }

    private static Optional<ShipmentRecord> findShipmentRecord(
            final String orderId, final String shipmentNumber) {
        final AttributeMapInstance<ShipmentRecord> instance =
                OrderFulfillmentFlow.shipmentRecords.instance(shipmentNumber);
        return client().getAttributes(orderId, instance).get(instance);
    }

    private static void await(final Supplier<Boolean> condition, final String message) {
        try {
            environment().awaitCondition(condition, Boolean::booleanValue, WAIT_TIMEOUT, message);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new AssertionError(interrupted);
        }
    }

    // The RPC fails fast while a Step holds the same Attribute lock.
    private static void retryOnLockConflict(final Runnable invoke) {
        final long deadline = System.nanoTime() + WAIT_TIMEOUT.toNanos();
        while (true) {
            try {
                invoke.run();
                return;
            } catch (final RpcLockConflictException conflict) {
                if (System.nanoTime() >= deadline) {
                    throw conflict;
                }
                try {
                    Thread.sleep(LOCK_CONFLICT_RETRY_MILLIS);
                } catch (final InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    throw new AssertionError(interrupted);
                }
            }
        }
    }

    private static void terminateFlows(final String... flowIds) {
        for (final String flowId : flowIds) {
            client().stopFlow(flowId, new StopFlowOptions(StopType.TERMINATE, ""));
        }
    }

    private static IntegEnvironment environment() {
        return SharedIntegExtension.environment();
    }

    private static Client client() {
        return environment().client();
    }
}
