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

package io.superdurable.dex.patterns.childflows;

import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.patterns.childflows.ShipmentFlow.ShipmentRequest;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

/**
 * Ships an order as one {@link ShipmentFlow} per shipment. Each shipment is a top-level Flow whose
 * Flow ID is its shipment number.
 */
@Component
public class OrderFulfillmentFlow implements Flow<OrderFulfillmentFlow.Order> {
    /** Each shipment's own Flow timeout. A closing order never stops its shipments. */
    public static final Duration SHIPMENT_TIMEOUT = Duration.ofDays(30);

    /** The record status of a shipment number that another request owns. */
    public static final String SHIPMENT_REJECTED = "rejected";

    /** The record status of a shipment that does not exist. */
    public static final String SHIPMENT_UNAVAILABLE = "unavailable";

    /**
     * The input of {@link OrderFulfillmentFlow}. The order number is the Flow ID.
     *
     * @param shipments the shipments of the order
     * @param reconcileIntervalSeconds how long the order waits for a report before it reads the
     *     shipment
     */
    public record Order(List<Shipment> shipments, int reconcileIntervalSeconds) {
    }

    /**
     * The input of the Steps that start and track one shipment.
     *
     * @param shipment the tracked shipment
     * @param reconcileIntervalSeconds how long to wait for a report before reading the shipment
     */
    public record TrackedShipment(Shipment shipment, int reconcileIntervalSeconds) {
    }

    /** Holds one record per shipment, keyed by shipment number. */
    public static final AttributeMap<ShipmentRecord> shipmentRecords =
            AttributeMap.define("shipmentRecords", ShipmentRecord.class);

    /** Receives each shipment's report, keyed by shipment number. */
    public static final ChannelMap<String> shipmentReports =
            ChannelMap.define("shipmentReports", String.class);

    private final StartShipments startShipments = new StartShipments();
    private final StartShipment startShipment;
    private final AwaitShipment awaitShipment;

    public OrderFulfillmentFlow(
            final ObjectProvider<Client> clientProvider, final ShipmentFlow shipmentFlow) {
        startShipment = new StartShipment(clientProvider, shipmentFlow);
        awaitShipment = new AwaitShipment(clientProvider);
    }

    @Override
    public StepList<Order> getSteps() {
        return StepList.startStep(startShipments).otherSteps(startShipment, awaitShipment);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(shipmentRecords, shipmentReports);
    }

    /**
     * Receives a shipment's report.
     *
     * @param context the RPC context
     * @param report the shipment's report
     */
    @RPC
    public void recordShipmentOutcome(final Context context, final ShipmentReport report) {
        shipmentReports.publish(context, report.shipmentNumber(), report.outcome());
    }

    /**
     * Returns the lowercase status name, matching the other example languages.
     *
     * @param status a Flow status
     * @return the status name, such as "running" or "canceled"
     */
    static String statusName(final FlowStatus status) {
        return switch (status) {
            case RUNNING -> "running";
            case COMPLETED -> "completed";
            case FAILED -> "failed";
            case CANCELED -> "canceled";
            case TERMINATED -> "terminated";
            case SERVER_SIDE_TIMEOUT_INTERNAL_ONLY -> "server-side timeout (internal only)";
        };
    }

    // dex:group group-id:start group-label:"Start shipments"
    // dex:explanation text:"Starts one branch per shipment in the order."
    static final class StartShipments implements Step<Order> {
        @Override
        public Class<Order> getInputType() {
            return Order.class;
        }

        @Override
        public StepDecision execute(final Context context, final Order order) {
            if (order.shipments() == null || order.shipments().isEmpty()) {
                return StepDecision.gracefulComplete();
            }
            final List<StepMovement<?>> movements = new ArrayList<StepMovement<?>>();
            for (final Shipment shipment : order.shipments()) {
                movements.add(StepMovement.of(StartShipment.class,
                        new TrackedShipment(shipment, order.reconcileIntervalSeconds())));
            }
            return StepDecision.goToMany(movements);
        }
    }

    // dex:group group-id:start group-label:"Start shipments"
    // dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number."
    static final class StartShipment implements Step<TrackedShipment> {
        private final ObjectProvider<Client> clientProvider;
        private final ShipmentFlow shipmentFlow;

        StartShipment(final ObjectProvider<Client> clientProvider, final ShipmentFlow shipmentFlow) {
            this.clientProvider = clientProvider;
            this.shipmentFlow = shipmentFlow;
        }

        @Override
        public Class<TrackedShipment> getInputType() {
            return TrackedShipment.class;
        }

        @Override
        public StepDecision execute(final Context context, final TrackedShipment tracked) {
            final Client client = clientProvider.getObject();
            final String number = tracked.shipment().shipmentNumber();
            // Derived from this order and shipment, so a retried Execute repeats the same start.
            final String requestId = "start:" + context.getFlowId() + ":" + number;
            try {
                client.startFlow(
                        shipmentFlow,
                        number,
                        new ShipmentRequest(context.getFlowId(), tracked.shipment()),
                        StartFlowOptions.newBuilder()
                                .timeout(SHIPMENT_TIMEOUT)
                                .idReusePolicy(IdReusePolicy.DISALLOW)
                                .requestId(requestId)
                                .ignoreAlreadyStarted(true)
                                .build());
            } catch (final FlowAlreadyStartedException alreadyStarted) {
                // Another request owns this shipment number; this order does not track it.
                shipmentRecords.set(context, number, new ShipmentRecord(SHIPMENT_REJECTED, null));
                return StepDecision.gracefulComplete();
            }
            return StepDecision.goTo(AwaitShipment.class, tracked);
        }
    }

    // dex:group group-id:track group-label:"Track shipment"
    // dex:explanation text:"Records the shipment's report, or reads the shipment when no report arrives in time."
    static final class AwaitShipment implements Step<TrackedShipment> {
        private final ObjectProvider<Client> clientProvider;

        AwaitShipment(final ObjectProvider<Client> clientProvider) {
            this.clientProvider = clientProvider;
        }

        @Override
        public Class<TrackedShipment> getInputType() {
            return TrackedShipment.class;
        }

        @Override
        public Wait waitFor(final Context context, final TrackedShipment tracked) {
            return Wait.anyOf(
                    shipmentReports.forOne(tracked.shipment().shipmentNumber()),
                    Timer.byDuration(Duration.ofSeconds(tracked.reconcileIntervalSeconds())));
        }

        @Override
        public StepDecision execute(final Context context, final TrackedShipment tracked) {
            final String number = tracked.shipment().shipmentNumber();
            final List<String> outcomes = shipmentReports.getConditionResults(context, number);
            final ShipmentRecord record;
            if (!outcomes.isEmpty()) {
                record = new ShipmentRecord(null, outcomes.get(0));
            } else {
                record = reconcileShipment(clientProvider.getObject(), number);
                if (statusName(FlowStatus.RUNNING).equals(record.status())) {
                    return StepDecision.goTo(AwaitShipment.class, tracked);
                }
            }
            shipmentRecords.set(context, number, record);
            return StepDecision.gracefulComplete();
        }

        /**
         * Reads a shipment that has not reported: its status first, then its outcome.
         *
         * @return a "running" record while the shipment runs, an "unavailable" record when the
         *     shipment does not exist, or the terminal status with the stored outcome; any other
         *     failure throws so the Step retries
         */
        private static ShipmentRecord reconcileShipment(final Client client, final String number) {
            final FlowInfo info;
            try {
                info = client.describeFlow(number);
            } catch (final FlowNotFoundException notFound) {
                return new ShipmentRecord(SHIPMENT_UNAVAILABLE, null);
            }
            final String status = statusName(info.getStatus());
            if (info.getStatus() == FlowStatus.RUNNING) {
                return new ShipmentRecord(status, null);
            }
            try {
                // An unset outcome Attribute leaves the outcome empty: the shipment ended without one.
                final String outcome = client.getAttributes(number, ShipmentFlow.shipmentOutcome)
                        .get(ShipmentFlow.shipmentOutcome)
                        .orElse(null);
                return new ShipmentRecord(status, outcome);
            } catch (final FlowNotFoundException notFound) {
                return new ShipmentRecord(status, null);
            }
        }
    }
}
