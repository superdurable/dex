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

package io.superdurable.dex.workflow.subscription;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.workflow.MyDependencyService;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class SubscriptionFlow implements Flow<Customer> {
    private static final String SUBSCRIPTION_OVER_KEY = "subscription-over";

    public final Attribute<Integer> billingPeriodNumber =
            Attribute.define("billing-period-number", Integer.class);
    public final Attribute<Customer> customerDetails =
            Attribute.define("customer", Customer.class);
    public final Channel<Void> cancelSubscription =
            Channel.define("cancel-subscription", Void.class);
    public final Channel<Integer> updateChargeAmount =
            Channel.define("update-charge-amount", Integer.class);

    private final MyDependencyService service;
    private final Initialize initialize = new Initialize();
    private final Trial trial = new Trial();
    private final ChargeCurrentBill chargeCurrentBill = new ChargeCurrentBill();
    private final Cancel cancel = new Cancel();
    private final UpdateChargeAmount updateChargeAmountStep = new UpdateChargeAmount();

    public SubscriptionFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<Customer> getSteps() {
        return StepList.startStep(initialize)
                .otherSteps(trial, chargeCurrentBill, cancel, updateChargeAmountStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                billingPeriodNumber,
                customerDetails,
                cancelSubscription,
                updateChargeAmount);
    }

    @RPC
    public RPCResult<Subscription> describe(final Context context) {
        return RPCResult.of(customerDetails.get(context).subscription);
    }

    final class Initialize implements Step<Customer> {
        @Override
        public Class<Customer> getInputType() {
            return Customer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Customer customer) {
            customerDetails.set(context, customer);
            return StepDecision.goToMulti(
                    StepMovement.of(trial, null),
                    StepMovement.of(cancel, null),
                    StepMovement.of(updateChargeAmountStep, null));
        }
    }

    final class Trial implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            final Customer customer = customerDetails.get(context);
            service.sendEmail(customer.email, "welcome email", "hello content");
            return Wait.allOf(Timer.byDuration(customer.subscription.trialPeriod));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            billingPeriodNumber.set(context, 0);
            return StepDecision.goTo(chargeCurrentBill, null);
        }
    }

    final class ChargeCurrentBill implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            final Customer customer = customerDetails.get(context);
            final int periodNumber = billingPeriodNumber.get(context);
            if (periodNumber >= customer.subscription.maxBillingPeriods) {
                context.setStepExecutionLocal(SUBSCRIPTION_OVER_KEY, true, Boolean.class);
                return Wait.skipImmediately();
            }
            billingPeriodNumber.set(context, periodNumber + 1);
            return Wait.allOf(Timer.byDuration(customer.subscription.billingPeriod));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final Customer customer = customerDetails.get(context);
            final boolean subscriptionOver =
                    Boolean.TRUE.equals(
                            context.getStepExecutionLocal(SUBSCRIPTION_OVER_KEY, Boolean.class));
            if (subscriptionOver) {
                service.sendEmail(customer.email, "subscription over", "hello content");
                return StepDecision.forceComplete("subscription ended");
            }
            service.chargeUser(
                    customer.email,
                    customer.id,
                    customer.subscription.billingPeriodCharge);
            return StepDecision.goTo(chargeCurrentBill, null);
        }
    }

    final class Cancel implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.allOf(cancelSubscription.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final Customer customer = customerDetails.get(context);
            service.sendEmail(customer.email, "subscription canceled", "hello content");
            return StepDecision.forceComplete("subscription canceled");
        }
    }

    final class UpdateChargeAmount implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.allOf(updateChargeAmount.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final List<Integer> amounts = updateChargeAmount.getConditionResults(context);
            if (amounts.size() != 1) {
                throw new IllegalStateException(
                        "expected one charge amount, got " + amounts.size());
            }
            final Customer customer = customerDetails.get(context);
            customer.subscription.billingPeriodCharge = amounts.get(0);
            customerDetails.set(context, customer);
            return StepDecision.goTo(updateChargeAmountStep, null);
        }
    }
}
