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

package io.superdurable.dex.patterns.childflowsbusinessidentity;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.AttributeMap;
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
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.patterns.childflowsbusinessidentity.ShipmentFlow.ShipmentRequest;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Optional;
import java.util.Set;

/**
 * Ships an order as one {@link ShipmentFlow} per shipment. Each shipment is a top-level Flow whose
 * Flow ID is its shipment number.
 */
@Component
public class OrderFulfillmentFlow implements Flow<OrderFulfillmentFlow.Order> {
    /** Each shipment's own Flow timeout. A closing order never stops its shipments. */
    public static final Duration SHIPMENT_TIMEOUT = Duration.ofDays(30);

    /** The record status of a shipment of the order that has no claimed run yet. */
    public static final String SHIPMENT_STARTING = "starting";

    /** The record status of a shipment whose run this order started and claimed. */
    public static final String SHIPMENT_STARTED = "started";

    /** The record status of a shipment number that another request owns. */
    public static final String SHIPMENT_REJECTED = "rejected";

    /** The record status of a claimed run that can no longer be read. */
    public static final String SHIPMENT_UNAVAILABLE = "unavailable";

    /** The registered Flow type of {@link ShipmentFlow}. */
    public static final String SHIPMENT_FLOW_TYPE = "ShipmentFlow";

    private static final String DUPLICATE_SHIPMENT_PREFIX = "duplicate shipment number: ";

    private static final String NEGATIVE_DELIVERY_DELAY_PREFIX =
            "negative delivery delay for shipment: ";

    private static final String INVALID_RECONCILE_INTERVAL_MESSAGE =
            "reconcile interval must be positive";

    private static final ObjectMapper START_REQUEST_MAPPER = new ObjectMapper();

    /**
     * The input of {@link OrderFulfillmentFlow}. The order number is the Flow ID.
     *
     * @param shipments the shipments of the order
     * @param reconcileIntervalSeconds how often the order reads a shipment that has not ended
     */
    public record Order(List<Shipment> shipments, int reconcileIntervalSeconds) {
    }

    /**
     * The input of the Steps that start and track one shipment.
     *
     * @param shipment the tracked shipment
     * @param reconcileIntervalSeconds how often the order reads the shipment while it runs
     * @param shipmentRunId the run this order started and tracks; {@code null} before the start
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    public record TrackedShipment(
            Shipment shipment, int reconcileIntervalSeconds, String shipmentRunId) {
    }

    /**
     * What {@link StartShipment} hands to {@link ClaimShipment}.
     *
     * @param tracked the started shipment, with its run ID unless it was rejected
     * @param rejected true when another request owns the shipment number
     */
    public record StartedShipment(TrackedShipment tracked, boolean rejected) {
    }

    /**
     * A claimed shipment run that ended, with what the order read from it.
     *
     * @param shipmentNumber the shipment number
     * @param shipmentRunId the claimed run
     * @param reading the terminal status and the outcome the order read
     */
    public record EndedShipment(
            String shipmentNumber, String shipmentRunId, ShipmentRecord reading) {
    }

    /** Holds one record per shipment, keyed by shipment number. */
    public static final AttributeMap<ShipmentRecord> shipmentRecords =
            AttributeMap.define("shipmentRecords", ShipmentRecord.class);

    /**
     * The early reports of one shipment: each shipment run ID maps to that run's first report.
     */
    public static final class PendingShipmentReports extends LinkedHashMap<String, ShipmentReport> {
        private static final long serialVersionUID = 1L;
    }

    /** Holds reports that arrive before the claim, keyed by shipment number. */
    public static final AttributeMap<PendingShipmentReports> pendingShipmentReports =
            AttributeMap.define("pendingShipmentReports", PendingShipmentReports.class);

    /** Serializes every write to shipmentRecords and pendingShipmentReports; it stores no value. */
    public static final Attribute<Boolean> shipmentRecordsLock =
            Attribute.define("shipmentRecordsLock", Boolean.class);

    private final StartShipments startShipments = new StartShipments();
    private final StartShipment startShipment;
    private final ClaimShipment claimShipment = new ClaimShipment();
    private final AwaitShipment awaitShipment;
    private final RecordShipmentEnd recordShipmentEnd = new RecordShipmentEnd();

    public OrderFulfillmentFlow(
            final ObjectProvider<Client> clientProvider, final ShipmentFlow shipmentFlow) {
        startShipment = new StartShipment(clientProvider, shipmentFlow);
        awaitShipment = new AwaitShipment(clientProvider);
    }

    @Override
    public StepList<Order> getSteps() {
        return StepList.startStep(startShipments)
                .otherSteps(startShipment, claimShipment, awaitShipment, recordShipmentEnd);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(shipmentRecords, pendingShipmentReports, shipmentRecordsLock);
    }

    /**
     * Fills the outcome of a shipment's record. It accepts only the run this order claimed; the
     * first report wins, and any other report changes nothing. Before the claim, it keeps the
     * first report of each run for {@link ClaimShipment} to merge.
     *
     * @param context the RPC context
     * @param report the shipment run's report
     */
    @RPC(
            lockAttributes = {"shipmentRecordsLock"},
            loadAttributeMaps = {"shipmentRecords", "pendingShipmentReports"})
    public void recordShipmentOutcome(final Context context, final ShipmentReport report) {
        final Optional<ShipmentRecord> found = loadShipmentRecord(context, report.shipmentNumber());
        // Ignore an unknown shipment and a report without a run.
        if (found.isEmpty() || report.shipmentRunId() == null || report.shipmentRunId().isEmpty()) {
            return;
        }
        if (SHIPMENT_STARTING.equals(found.get().status())) {
            final PendingShipmentReports stored =
                    pendingShipmentReports.get(context, report.shipmentNumber());
            final PendingShipmentReports pending =
                    stored != null ? stored : new PendingShipmentReports();
            if (pending.putIfAbsent(report.shipmentRunId(), report) == null) {
                pendingShipmentReports.set(context, report.shipmentNumber(), pending);
            }
            return;
        }
        // Ignore a rejected shipment, another run, and a repeated report.
        if (found.get().runId() == null
                || !found.get().runId().equals(report.shipmentRunId())
                || found.get().outcome() != null) {
            return;
        }
        final ShipmentRecord record = found.get();
        shipmentRecords.set(context, report.shipmentNumber(),
                new ShipmentRecord(record.status(), record.runId(), report.outcome()));
    }

    /**
     * Derives the start request ID from the complete start request. A retried Execute sends the
     * same ID, so it repeats the same start. Any other request for the same shipment number sends
     * another ID, so Dex rejects it as already started.
     *
     * @param orderFlowId the Flow ID of the order that starts the shipment
     * @param shipment the shipment to start
     * @return "start:" followed by the lowercase hex SHA-256 of the encoded start request
     */
    public static String shipmentStartRequestId(final String orderFlowId, final Shipment shipment) {
        final List<Object> request = List.of(
                SHIPMENT_FLOW_TYPE,
                shipment.shipmentNumber(),
                orderFlowId,
                shipment.destination(),
                shipment.deliveryDelaySeconds(),
                SHIPMENT_TIMEOUT.toSeconds(),
                "disallow");
        try {
            final byte[] encoded = START_REQUEST_MAPPER.writeValueAsString(request)
                    .getBytes(StandardCharsets.UTF_8);
            return "start:" + HexFormat.of().formatHex(
                    MessageDigest.getInstance("SHA-256").digest(encoded));
        } catch (final JsonProcessingException | NoSuchAlgorithmException failure) {
            throw new IllegalStateException("cannot derive the shipment start request ID", failure);
        }
    }

    /**
     * Returns the loaded record of a shipment and whether it exists.
     *
     * @param context a context that loaded the shipmentRecords AttributeMap
     * @param number the shipment number
     * @return the shipment's record, or empty when the order has no record of that number
     */
    private static Optional<ShipmentRecord> loadShipmentRecord(
            final Context context, final String number) {
        return Optional.ofNullable(shipmentRecords.get(context, number));
    }

    /**
     * Takes the records lock for Execute, so record writes never race a report.
     *
     * @return Step options that lock shipmentRecordsLock for Execute
     */
    private static StepOptions lockShipmentRecords() {
        return StepOptions.newBuilder()
                .addExecuteLock(AttributeLock.of(shipmentRecordsLock))
                .build();
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
    // dex:explanation text:"Checks the order, registers its shipment numbers, then starts one branch per shipment."
    static final class StartShipments implements Step<Order> {
        @Override
        public Class<Order> getInputType() {
            return Order.class;
        }

        /** Takes the records lock, so registration never races a report. */
        @Override
        public StepOptions getStepOptions() {
            return lockShipmentRecords();
        }

        @Override
        public StepDecision execute(final Context context, final Order order) {
            if (order.reconcileIntervalSeconds() <= 0) {
                return StepDecision.forceFail(INVALID_RECONCILE_INTERVAL_MESSAGE);
            }
            if (order.shipments() == null || order.shipments().isEmpty()) {
                return StepDecision.gracefulComplete();
            }
            final Set<String> numbers = new HashSet<String>();
            for (final Shipment shipment : order.shipments()) {
                if (shipment.deliveryDelaySeconds() < 0) {
                    return StepDecision.forceFail(
                            NEGATIVE_DELIVERY_DELAY_PREFIX + shipment.shipmentNumber());
                }
                if (!numbers.add(shipment.shipmentNumber())) {
                    return StepDecision.forceFail(
                            DUPLICATE_SHIPMENT_PREFIX + shipment.shipmentNumber());
                }
            }
            final List<StepMovement<?>> movements = new ArrayList<StepMovement<?>>();
            for (final Shipment shipment : order.shipments()) {
                shipmentRecords.set(context, shipment.shipmentNumber(),
                        new ShipmentRecord(SHIPMENT_STARTING, null, null));
                movements.add(StepMovement.of(StartShipment.class,
                        new TrackedShipment(shipment, order.reconcileIntervalSeconds(), null)));
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

        /** Takes no lock: a report must never wait for the start call or its retries. */
        @Override
        public StepDecision execute(final Context context, final TrackedShipment tracked) {
            final Client client = clientProvider.getObject();
            final String number = tracked.shipment().shipmentNumber();
            final String requestId = shipmentStartRequestId(context.getFlowId(), tracked.shipment());
            final String runId;
            try {
                runId = client.startFlow(
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
                return StepDecision.goTo(
                        ClaimShipment.class, new StartedShipment(tracked, true));
            }
            return StepDecision.goTo(ClaimShipment.class, new StartedShipment(new TrackedShipment(
                    tracked.shipment(), tracked.reconcileIntervalSeconds(), runId), false));
        }
    }

    // dex:group group-id:start group-label:"Start shipments"
    // dex:explanation text:"Claims the started run and merges a report that arrived before the claim."
    static final class ClaimShipment implements Step<StartedShipment> {
        @Override
        public Class<StartedShipment> getInputType() {
            return StartedShipment.class;
        }

        /** Takes the records lock, so the claim never races a report. */
        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLock(AttributeLock.of(shipmentRecordsLock))
                    .addExecuteLoadAttributeMap(pendingShipmentReports)
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final StartedShipment started) {
            final String number = started.tracked().shipment().shipmentNumber();
            final PendingShipmentReports pending = pendingShipmentReports.get(context, number);
            // The claim or the rejection settles every early report of this shipment.
            if (pending != null) {
                pendingShipmentReports.delete(context, number);
            }
            if (started.rejected()) {
                shipmentRecords.set(
                        context, number, new ShipmentRecord(SHIPMENT_REJECTED, null, null));
                return StepDecision.gracefulComplete();
            }
            final String runId = started.tracked().shipmentRunId();
            // Claim the run: reports and reads of any other run of this number are ignored.
            final ShipmentReport report = pending != null ? pending.get(runId) : null;
            final String outcome = report != null ? report.outcome() : null;
            shipmentRecords.set(context, number, new ShipmentRecord(SHIPMENT_STARTED, runId, outcome));
            return StepDecision.goTo(AwaitShipment.class, started.tracked());
        }
    }

    // dex:group group-id:track group-label:"Track shipment"
    // dex:explanation text:"Reads the claimed shipment run at each interval until it ends."
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
            return Wait.until(
                    Timer.byDuration(Duration.ofSeconds(tracked.reconcileIntervalSeconds())));
        }

        /** Takes no lock: a report must never wait for the reads or their retries. */
        @Override
        public StepDecision execute(final Context context, final TrackedShipment tracked) {
            final String number = tracked.shipment().shipmentNumber();
            final ShipmentRecord reading = reconcileShipment(
                    clientProvider.getObject(), number, tracked.shipmentRunId());
            if (statusName(FlowStatus.RUNNING).equals(reading.status())) {
                return StepDecision.goTo(AwaitShipment.class, tracked);
            }
            return StepDecision.goTo(RecordShipmentEnd.class,
                    new EndedShipment(number, tracked.shipmentRunId(), reading));
        }

        /**
         * Reads the claimed run of a shipment: its status first, then its outcome. It keeps an
         * outcome only when the run is still the claimed one after the read.
         *
         * @return a "running" record while that run runs, an "unavailable" record when that run no
         *     longer exists or another run replaced it, or the terminal status with the stored
         *     outcome; any other failure throws so the Step retries
         */
        private static ShipmentRecord reconcileShipment(
                final Client client, final String number, final String runId) {
            final Optional<FlowInfo> claimed = describeClaimedRun(client, number, runId);
            if (claimed.isEmpty()) {
                return new ShipmentRecord(SHIPMENT_UNAVAILABLE, null, null);
            }
            final FlowInfo info = claimed.get();
            final String status = statusName(info.getStatus());
            if (info.getStatus() == FlowStatus.RUNNING) {
                return new ShipmentRecord(status, null, null);
            }
            final String outcome;
            try {
                // An unset outcome Attribute leaves the outcome empty: the shipment ended without one.
                outcome = client.getAttributes(number, ShipmentFlow.shipmentOutcome)
                        .get(ShipmentFlow.shipmentOutcome)
                        .orElse(null);
            } catch (final FlowNotFoundException notFound) {
                return new ShipmentRecord(status, null, null);
            }
            // The Attribute read targets the latest run, so confirm it is still the claimed one.
            final boolean stillClaimed = describeClaimedRun(client, number, runId).isPresent();
            return new ShipmentRecord(status, null, stillClaimed ? outcome : null);
        }

        /**
         * Describes the latest run of a shipment.
         *
         * @return the run's Flow info, or empty when the shipment does not exist or its latest run
         *     is not {@code runId}; any other failure throws
         */
        private static Optional<FlowInfo> describeClaimedRun(
                final Client client, final String number, final String runId) {
            final FlowInfo info;
            try {
                info = client.describeFlow(number);
            } catch (final FlowNotFoundException notFound) {
                return Optional.empty();
            }
            return runId != null && runId.equals(info.getRunId())
                    ? Optional.of(info)
                    : Optional.empty();
        }
    }

    // dex:group group-id:track group-label:"Track shipment"
    // dex:explanation text:"Records how the claimed shipment run ended."
    static final class RecordShipmentEnd implements Step<EndedShipment> {
        @Override
        public Class<EndedShipment> getInputType() {
            return EndedShipment.class;
        }

        /** Takes the same lock as the report RPC and loads the records. */
        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLock(AttributeLock.of(shipmentRecordsLock))
                    .addExecuteLoadAttributeMap(shipmentRecords)
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final EndedShipment ended) {
            // Merge with the record, which may already hold the reported outcome.
            final String storedOutcome = loadShipmentRecord(context, ended.shipmentNumber())
                    .map(ShipmentRecord::outcome)
                    .orElse(null);
            final String outcome = storedOutcome != null ? storedOutcome : ended.reading().outcome();
            shipmentRecords.set(context, ended.shipmentNumber(),
                    new ShipmentRecord(ended.reading().status(), ended.shipmentRunId(), outcome));
            return StepDecision.gracefulComplete();
        }
    }
}
