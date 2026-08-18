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

package io.superdurable.dex.products.subscription;

import io.superdurable.dex.shared.MyDependencyService;

import java.time.Duration;
import java.util.List;

final class SubscriptionBilling {
    private SubscriptionBilling() {
    }

    static void sendWelcomeEmail(final Customer customer, final MyDependencyService service) {
        service.sendEmail(customer.email, "welcome email", "hello content");
    }

    static Duration trialPeriod(final Customer customer) {
        return customer.subscription.trialPeriod;
    }

    static boolean isSubscriptionOver(final Customer customer, final int periodNumber) {
        return periodNumber >= customer.subscription.maxBillingPeriods;
    }

    static Duration billingPeriod(final Customer customer) {
        return customer.subscription.billingPeriod;
    }

    static void sendSubscriptionOverEmail(
            final Customer customer, final MyDependencyService service) {
        service.sendEmail(customer.email, "subscription over", "hello content");
    }

    static void chargeCurrentPeriod(final Customer customer, final MyDependencyService service) {
        service.chargeUser(
                customer.email,
                customer.id,
                customer.subscription.billingPeriodCharge);
    }

    static void sendCanceledEmail(final Customer customer, final MyDependencyService service) {
        service.sendEmail(customer.email, "subscription canceled", "hello content");
    }

    static int requireSingleChargeAmount(final List<Integer> amounts) {
        if (amounts == null || amounts.size() != 1) {
            final int size = amounts == null ? 0 : amounts.size();
            throw new IllegalStateException("expected one charge amount, got " + size);
        }
        return amounts.get(0);
    }

    static void applyChargeAmount(final Customer customer, final int amount) {
        customer.subscription.billingPeriodCharge = amount;
    }
}
