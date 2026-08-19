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
import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

public class SubscriptionBillingTest {
    private static final Customer TEST_CUSTOMER = new Customer(
            "Quanzheng",
            "Long",
            "123",
            "qlong.seattle@gmail.com",
            new Subscription(
                    Duration.ofSeconds(2),
                    Duration.ofSeconds(1),
                    10,
                    100));

    @Test
    void sendWelcomeEmail() {
        final RecordingService service = new RecordingService();

        SubscriptionBilling.sendWelcomeEmail(TEST_CUSTOMER, service);

        assertEquals(
                Collections.singletonList(new RecordedEmail(
                        TEST_CUSTOMER.email,
                        "welcome email",
                        "hello content")),
                service.emails);
        assertEquals(Duration.ofSeconds(2), SubscriptionBilling.trialPeriod(TEST_CUSTOMER));
    }

    @Test
    void isSubscriptionOver() {
        assertFalse(SubscriptionBilling.isSubscriptionOver(TEST_CUSTOMER, 0));
        assertTrue(SubscriptionBilling.isSubscriptionOver(
                TEST_CUSTOMER,
                TEST_CUSTOMER.subscription.maxBillingPeriods));
        assertEquals(Duration.ofSeconds(1), SubscriptionBilling.billingPeriod(TEST_CUSTOMER));
    }

    @Test
    void chargeCurrentPeriod() {
        final RecordingService service = new RecordingService();

        SubscriptionBilling.chargeCurrentPeriod(TEST_CUSTOMER, service);

        assertEquals(
                Collections.singletonList(new RecordedCharge(
                        TEST_CUSTOMER.email,
                        TEST_CUSTOMER.id,
                        100)),
                service.charges);
        assertTrue(service.emails.isEmpty());
    }

    @Test
    void sendSubscriptionOverEmail() {
        final RecordingService service = new RecordingService();

        SubscriptionBilling.sendSubscriptionOverEmail(TEST_CUSTOMER, service);

        assertTrue(service.charges.isEmpty());
        assertEquals(
                Collections.singletonList(new RecordedEmail(
                        TEST_CUSTOMER.email,
                        "subscription over",
                        "hello content")),
                service.emails);
    }

    @Test
    void sendCanceledEmail() {
        final RecordingService service = new RecordingService();

        SubscriptionBilling.sendCanceledEmail(TEST_CUSTOMER, service);

        assertEquals(
                Collections.singletonList(new RecordedEmail(
                        TEST_CUSTOMER.email,
                        "subscription canceled",
                        "hello content")),
                service.emails);
    }

    @Test
    void applyChargeAmount() {
        final Customer customer = new Customer(
                TEST_CUSTOMER.firstName,
                TEST_CUSTOMER.lastName,
                TEST_CUSTOMER.id,
                TEST_CUSTOMER.email,
                new Subscription(
                        TEST_CUSTOMER.subscription.trialPeriod,
                        TEST_CUSTOMER.subscription.billingPeriod,
                        TEST_CUSTOMER.subscription.maxBillingPeriods,
                        TEST_CUSTOMER.subscription.billingPeriodCharge));

        SubscriptionBilling.applyChargeAmount(customer, 200);

        assertEquals(200, customer.subscription.billingPeriodCharge);
        assertEquals(200, SubscriptionBilling.requireSingleChargeAmount(
                Collections.singletonList(200)));
    }

    @Test
    void requireSingleChargeAmountRejectsUnexpectedResults() {
        assertThrows(
                IllegalStateException.class,
                () -> SubscriptionBilling.requireSingleChargeAmount(null));
        assertThrows(
                IllegalStateException.class,
                () -> SubscriptionBilling.requireSingleChargeAmount(Collections.emptyList()));
        assertThrows(
                IllegalStateException.class,
                () -> SubscriptionBilling.requireSingleChargeAmount(Arrays.asList(100, 200)));
    }

    private static final class RecordedEmail {
        private final String recipient;
        private final String subject;
        private final String content;

        private RecordedEmail(
                final String recipient, final String subject, final String content) {
            this.recipient = recipient;
            this.subject = subject;
            this.content = content;
        }

        @Override
        public boolean equals(final Object other) {
            if (!(other instanceof RecordedEmail)) {
                return false;
            }
            final RecordedEmail that = (RecordedEmail) other;
            return recipient.equals(that.recipient)
                    && subject.equals(that.subject)
                    && content.equals(that.content);
        }

        @Override
        public int hashCode() {
            return Arrays.hashCode(new Object[] {recipient, subject, content});
        }
    }

    private static final class RecordedCharge {
        private final String email;
        private final String customerId;
        private final int amount;

        private RecordedCharge(final String email, final String customerId, final int amount) {
            this.email = email;
            this.customerId = customerId;
            this.amount = amount;
        }

        @Override
        public boolean equals(final Object other) {
            if (!(other instanceof RecordedCharge)) {
                return false;
            }
            final RecordedCharge that = (RecordedCharge) other;
            return email.equals(that.email)
                    && customerId.equals(that.customerId)
                    && amount == that.amount;
        }

        @Override
        public int hashCode() {
            return Arrays.hashCode(new Object[] {email, customerId, amount});
        }
    }

    private static final class RecordingService extends MyDependencyService {
        private final List<RecordedEmail> emails = new ArrayList<RecordedEmail>();
        private final List<RecordedCharge> charges = new ArrayList<RecordedCharge>();

        @Override
        public void sendEmail(
                final String recipient, final String subject, final String content) {
            emails.add(new RecordedEmail(recipient, subject, content));
        }

        @Override
        public void chargeUser(final String email, final String customerId, final int amount) {
            charges.add(new RecordedCharge(email, customerId, amount));
        }
    }
}
