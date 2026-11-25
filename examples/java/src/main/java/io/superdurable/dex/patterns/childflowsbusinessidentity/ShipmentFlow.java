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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;

/**
 * Delivers one shipment. Its Flow ID is the shipment number, so people and systems can look it
 * up, retry it, or stop it by that number.
 */
@Component
public class ShipmentFlow implements Flow<ShipmentFlow.ShipmentRequest> {
    /** The shipment's committed outcome. The order reads it when no report arrives. */
    public static final Attribute<String> shipmentOutcome =
            Attribute.define("shipmentOutcome", String.class);

    /**
     * The input of {@link ShipmentFlow}.
     *
     * @param orderFlowId the Flow ID of the order that started this shipment
     * @param shipment the shipment to deliver
     */
    public record ShipmentRequest(String orderFlowId, Shipment shipment) {
    }

    /**
     * The report a shipment still has to send to its order.
     *
     * @param orderFlowId the Flow ID of the order to report to
     * @param report the report to send
     */
    public record PendingReport(String orderFlowId, ShipmentReport report) {
    }

    private final DeliverShipment deliverShipment = new DeliverShipment();
    private final ReportShipmentOutcome reportShipmentOutcome;

    public ShipmentFlow(final ObjectProvider<Client> clientProvider) {
        reportShipmentOutcome = new ReportShipmentOutcome(clientProvider);
    }

    @Override
    public StepList<ShipmentRequest> getSteps() {
        return StepList.startStep(deliverShipment).otherSteps(reportShipmentOutcome);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(shipmentOutcome);
    }

    // dex:group group-id:deliver group-label:"Deliver"
    // dex:explanation text:"Waits for the carrier, then commits the delivered outcome."
    static final class DeliverShipment implements Step<ShipmentRequest> {
        @Override
        public Class<ShipmentRequest> getInputType() {
            return ShipmentRequest.class;
        }

        /**
         * Stores the outcome before the report Step starts, even when the Flow defaults to ASYNC.
         */
        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder().executeDurability(StepDurability.SYNC).build();
        }

        @Override
        public Wait waitFor(final Context context, final ShipmentRequest request) {
            return Wait.until(Timer.byDuration(
                    Duration.ofSeconds(request.shipment().deliveryDelaySeconds())));
        }

        @Override
        public StepDecision execute(final Context context, final ShipmentRequest request) {
            final String outcome = "delivered to " + request.shipment().destination();
            shipmentOutcome.set(context, outcome);
            return StepDecision.goTo(ReportShipmentOutcome.class, new PendingReport(
                    request.orderFlowId(),
                    new ShipmentReport(request.shipment().shipmentNumber(), outcome)));
        }
    }

    // dex:group group-id:report group-label:"Report"
    // dex:explanation text:"Reports the committed outcome to the order that started this shipment."
    static final class ReportShipmentOutcome implements Step<PendingReport> {
        private final ObjectProvider<Client> clientProvider;

        ReportShipmentOutcome(final ObjectProvider<Client> clientProvider) {
            this.clientProvider = clientProvider;
        }

        @Override
        public Class<PendingReport> getInputType() {
            return PendingReport.class;
        }

        /** Keeps the report SYNC, like the Step that stores the outcome. */
        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder().executeDurability(StepDurability.SYNC).build();
        }

        @Override
        public StepDecision execute(final Context context, final PendingReport pending) {
            final Client client = clientProvider.getObject();
            // This SDK pin has no RPC request ID. The locked RPC keeps the first outcome,
            // so a repeat changes nothing.
            try {
                client.invokeRPC(
                        client.newRpcStub(OrderFulfillmentFlow.class, pending.orderFlowId())
                                ::recordShipmentOutcome,
                        pending.report());
            } catch (final FlowNotFoundException | FlowNotActiveOrNotFoundException closed) {
                // A closed order ends the attempts; the outcome stays readable through the Client.
            }
            return StepDecision.gracefulComplete(pending.report().outcome());
        }
    }
}
