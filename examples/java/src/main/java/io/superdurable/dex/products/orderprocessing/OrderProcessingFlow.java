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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class OrderProcessingFlow implements Flow<OrderRequest> {
    public final Attribute<String> orderStatus = Attribute.define(
            "order-status",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    public final Channel<String> sellerOk = Channel.define("seller-ok", String.class);

    private final MyDependencyService service;
    private final ChargeStep charge;
    private final ShipStep ship;
    private final RefundStep refund;

    public OrderProcessingFlow(final MyDependencyService service) {
        this.service = service;
        this.charge = new ChargeStep(service);
        this.ship = new ShipStep(service);
        this.refund = new RefundStep(service);
    }

    @Override
    public StepList<OrderRequest> getSteps() {
        return StepList.startStep(charge).otherSteps(ship, refund);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(orderStatus, sellerOk);
    }

    @RPC
    public RPCResult<String> approve(final Context context, final String note) {
        sellerOk.publish(context, "approved");
        return RPCResult.of("ok");
    }

    @RPC
    public RPCResult<String> describe(final Context context) {
        return RPCResult.of(orderStatus.get(context));
    }

    final class ChargeStep implements Step<OrderRequest> {
        private final MyDependencyService service;

        ChargeStep(final MyDependencyService service) {
            this.service = service;
        }

        @Override
        public String getStepType() {
            return "ChargeStep";
        }

        @Override
        public Class<OrderRequest> getInputType() {
            return OrderRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            // .totalDuration(Duration.ofHours(1))
                            .totalDuration(Duration.ofSeconds(3))
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final OrderRequest order) {
            service.chargeUser(order.email, order.customerId, order.amount);
            orderStatus.set(context, "charged");
            return StepDecision.goTo(ship, order);
        }
    }

    final class ShipStep implements Step<OrderRequest> {
        private final MyDependencyService service;

        ShipStep(final MyDependencyService service) {
            this.service = service;
        }

        @Override
        public String getStepType() {
            return "ShipStep";
        }

        @Override
        public Class<OrderRequest> getInputType() {
            return OrderRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            // .totalDuration(Duration.ofHours(1))
                            .totalDuration(Duration.ofSeconds(3))
                            .build())
                    .onExecuteFailureProceedTo(
                            refund,
                            StepOptions.newBuilder()
                                    .executeRetry(RetryPolicy.newBuilder()
                                            // .totalDuration(Duration.ofHours(1))
                                            .totalDuration(Duration.ofSeconds(3))
                                            .build())
                                    .build())
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final OrderRequest order) {
            return Wait.anyOf(
                    sellerOk.forOne(),
                    Timer.byDuration(Duration.ofHours(24)));
        }

        @Override
        public StepDecision execute(final Context context, final OrderRequest order) {
            if (context.hasTimerFired()) {
                service.sendEmail(
                        order.email,
                        "Reminder: approve shipment",
                        "Please approve or provide a tracking number.");
                return StepDecision.goTo(ship, order);
            }
            service.shipItem(order.orderId, order.testFailAtShipping);
            orderStatus.set(context, "shipped");
            return StepDecision.gracefulComplete("shipped:" + order.orderId);
        }
    }

    final class RefundStep implements Step<OrderRequest> {
        private final MyDependencyService service;

        RefundStep(final MyDependencyService service) {
            this.service = service;
        }

        @Override
        public String getStepType() {
            return "RefundStep";
        }

        @Override
        public Class<OrderRequest> getInputType() {
            return OrderRequest.class;
        }

        @Override
        public StepDecision execute(final Context context, final OrderRequest order) {
            service.updateExternalSystem("refund " + order.orderId);
            orderStatus.set(context, "refunded");
            return StepDecision.gracefulComplete("refunded:" + order.orderId);
        }
    }
}
