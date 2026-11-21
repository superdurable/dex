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

package io.superdurable.dex.products.customerrefund;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.products.customerrefund.FakeBillingService.Order;

import java.time.Duration;

/** Checks a refund request, asks a manager when needed, refunds, and tells the customer. */
public class CustomerRefundFlow implements Flow<CustomerRefundFlow.RefundRequest> {
    public static final int REFUND_WINDOW_DAYS = 30;
    public static final double MANAGER_APPROVAL_LIMIT_USD = 1000.00;
    public static final String AWAITING_MANAGER = "awaiting-manager";

    public static final Attribute<String> CASE_STATUS = Attribute.define("case-status", String.class);
    public static final Attribute<String> CUSTOMER_EMAIL =
            Attribute.define("customer-email", String.class);
    public static final Attribute<String> REFUND_ID = Attribute.define("refund-id", String.class);
    public static final Attribute<String> CUSTOMER_MESSAGE =
            Attribute.define("customer-message", String.class);
    public static final Channel<ManagerDecision> MANAGER_DECISIONS =
            Channel.define("manager-decisions", ManagerDecision.class);

    public record RefundRequest(
            String customer,
            String customerEmail,
            String orderId,
            String reason,
            int paymentTimeoutsToSimulate) {
        public RefundRequest withOrderId(final String newOrderId) {
            return new RefundRequest(customer, customerEmail, newOrderId, reason, paymentTimeoutsToSimulate);
        }

        public RefundRequest withPaymentTimeoutsToSimulate(final int timeouts) {
            return new RefundRequest(customer, customerEmail, orderId, reason, timeouts);
        }
    }

    public record ManagerDecision(boolean approved, String reason) {
    }

    public record DecisionReason(String reason) {
    }

    public record RefundCase(
            RefundRequest request,
            Order order,
            String outcome,
            String note,
            String refundId,
            String message) {
        public static RefundCase open(final RefundRequest request, final Order order) {
            return new RefundCase(request, order, "", "", "", "");
        }

        public RefundCase withOutcome(final String newOutcome, final String newNote) {
            return new RefundCase(request, order, newOutcome, newNote, refundId, message);
        }

        public RefundCase withRefundId(final String newRefundId) {
            return new RefundCase(request, order, "refunded", note, newRefundId, message);
        }

        public RefundCase withMessage(final String newMessage) {
            return new RefundCase(request, order, outcome, note, refundId, newMessage);
        }
    }

    private final FakeBillingService billing;
    private final FakeEmailService email;
    private final MockMessageModel model;
    private final ReceiveRequest receiveRequest = new ReceiveRequest();
    private final CheckOrder checkOrder = new CheckOrder();
    private final CheckPolicy checkPolicy = new CheckPolicy();
    private final RequestManagerApproval requestManagerApproval = new RequestManagerApproval();
    private final IssueRefund issueRefund = new IssueRefund();
    private final WriteCustomerMessage writeCustomerMessage = new WriteCustomerMessage();
    private final SendMessage sendMessage = new SendMessage();
    private final CloseCase closeCase = new CloseCase();

    public CustomerRefundFlow(
            final FakeBillingService billing,
            final FakeEmailService email,
            final MockMessageModel model) {
        this.billing = billing;
        this.email = email;
        this.model = model;
    }

    @Override
    public StepList<RefundRequest> getSteps() {
        return StepList.startStep(receiveRequest).otherSteps(
                checkOrder,
                checkPolicy,
                requestManagerApproval,
                issueRefund,
                writeCustomerMessage,
                sendMessage,
                closeCase);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                CASE_STATUS, CUSTOMER_EMAIL, REFUND_ID, CUSTOMER_MESSAGE, MANAGER_DECISIONS);
    }

    @RPC(lockAttributes = {"case-status"})
    public void approveRefund(final Context context, final DecisionReason input) {
        requireAwaitingManager(context);
        CASE_STATUS.set(context, "approved");
        MANAGER_DECISIONS.publish(context, new ManagerDecision(true, input.reason()));
    }

    @RPC(lockAttributes = {"case-status"})
    public void rejectRefund(final Context context, final DecisionReason input) {
        requireAwaitingManager(context);
        CASE_STATUS.set(context, "rejected");
        MANAGER_DECISIONS.publish(context, new ManagerDecision(false, input.reason()));
    }

    private static void requireAwaitingManager(final Context context) {
        if (!AWAITING_MANAGER.equals(CASE_STATUS.get(context))) {
            throw new IllegalStateException("this refund is not waiting for a manager");
        }
    }

    /** Record the refund request and the customer's email address. */
    final class ReceiveRequest implements Step<RefundRequest> {
        @Override
        public Class<RefundRequest> getInputType() {
            return RefundRequest.class;
        }

        @Override
        public StepDecision execute(final Context context, final RefundRequest input) {
            CASE_STATUS.set(context, "received");
            CUSTOMER_EMAIL.set(context, input.customerEmail());
            return StepDecision.goTo(CheckOrder.class, input);
        }
    }

    /** Look up the order in billing. */
    final class CheckOrder implements Step<RefundRequest> {
        @Override
        public Class<RefundRequest> getInputType() {
            return RefundRequest.class;
        }

        @Override
        public StepDecision execute(final Context context, final RefundRequest input) {
            final Order order = billing.getOrder(input.orderId());
            return StepDecision.goTo(CheckPolicy.class, RefundCase.open(input, order));
        }
    }

    /** Refund, deny, or ask a manager, by the refund policy. */
    final class CheckPolicy implements Step<RefundCase> {
        @Override
        public Class<RefundCase> getInputType() {
            return RefundCase.class;
        }

        @Override
        public StepDecision execute(final Context context, final RefundCase input) {
            final Order order = input.order();
            if (order.daysSinceCharge() > REFUND_WINDOW_DAYS || order.outageHours() == 0) {
                CASE_STATUS.set(context, "denied");
                final String note =
                        "The charge is outside the 30-day window and no outage was recorded.";
                return StepDecision.goTo(WriteCustomerMessage.class, input.withOutcome("denied", note));
            }
            if (order.amountUsd() > MANAGER_APPROVAL_LIMIT_USD) {
                CASE_STATUS.set(context, AWAITING_MANAGER);
                return StepDecision.goTo(RequestManagerApproval.class, input);
            }
            return StepDecision.goTo(IssueRefund.class, input);
        }
    }

    /** Wait for a manager to approve or reject the refund. */
    final class RequestManagerApproval implements Step<RefundCase> {
        @Override
        public Class<RefundCase> getInputType() {
            return RefundCase.class;
        }

        @Override
        public Wait waitFor(final Context context, final RefundCase input) {
            return Wait.until(MANAGER_DECISIONS.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final RefundCase input) {
            final ManagerDecision decision = MANAGER_DECISIONS.getConditionResults(context).get(0);
            if (decision.approved()) {
                return StepDecision.goTo(IssueRefund.class, input);
            }
            CASE_STATUS.set(context, "denied");
            return StepDecision.goTo(
                    WriteCustomerMessage.class, input.withOutcome("denied", decision.reason()));
        }
    }

    /** Refund the order through billing, once per case. */
    final class IssueRefund implements Step<RefundCase> {
        @Override
        public Class<RefundCase> getInputType() {
            return RefundCase.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(Duration.ofSeconds(5))
                            .backoffCoefficient(1.0)
                            .maximumAttempts(10)
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final RefundCase input) {
            final boolean simulateTimeout =
                    context.getAttempt() <= input.request().paymentTimeoutsToSimulate();
            final String refundId = billing.refund(
                    input.order(), "refund-" + context.getFlowId(), simulateTimeout);
            REFUND_ID.set(context, refundId);
            CASE_STATUS.set(context, "refunded");
            return StepDecision.goTo(WriteCustomerMessage.class, input.withRefundId(refundId));
        }
    }

    /** Ask the model to write the customer message. */
    final class WriteCustomerMessage implements Step<RefundCase> {
        @Override
        public Class<RefundCase> getInputType() {
            return RefundCase.class;
        }

        @Override
        public StepDecision execute(final Context context, final RefundCase input) {
            final String message = model.writeMessage(
                    input.request().customer(),
                    input.outcome(),
                    input.order().amountUsd(),
                    input.note());
            CUSTOMER_MESSAGE.set(context, message);
            return StepDecision.goTo(SendMessage.class, input.withMessage(message));
        }
    }

    /** Email the message to the customer, once per case. */
    final class SendMessage implements Step<RefundCase> {
        @Override
        public Class<RefundCase> getInputType() {
            return RefundCase.class;
        }

        @Override
        public StepDecision execute(final Context context, final RefundCase input) {
            email.send(
                    input.request().customerEmail(),
                    input.message(),
                    "email-" + context.getFlowId());
            return StepDecision.goTo(CloseCase.class, input);
        }
    }

    /** Close the case with its outcome. */
    final class CloseCase implements Step<RefundCase> {
        @Override
        public Class<RefundCase> getInputType() {
            return RefundCase.class;
        }

        @Override
        public StepDecision execute(final Context context, final RefundCase input) {
            CASE_STATUS.set(context, "closed-" + input.outcome());
            return StepDecision.gracefulComplete(input.outcome());
        }
    }
}
