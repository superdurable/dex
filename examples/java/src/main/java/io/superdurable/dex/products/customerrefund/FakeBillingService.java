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

import java.nio.file.Path;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;

/** A fake billing service that keeps refunds in a JSON file, so a restarted Worker sees them. */
public class FakeBillingService {
    public static final Path DEFAULT_BILLING_LEDGER = Path.of(".customer-refund/billing-refunds.json");

    private static final Map<String, Order> ORDERS = Map.of(
            "INV-1249", new Order("INV-1249", "Studio plan renewal", 1249.00, 6, 31),
            "INV-0388", new Order("INV-0388", "Studio plan renewal", 1249.00, 47, 0),
            "INV-0049", new Order("INV-0049", "Extra seat", 49.00, 3, 31),
            "INV-0412", new Order("INV-0412", "Studio plan renewal", 1249.00, 47, 31),
            "INV-0517", new Order("INV-0517", "Extra seat", 49.00, 6, 0));

    private final LockedJsonLedger<RecordedRefund> ledger;
    private final AtomicInteger simulatedTimeouts = new AtomicInteger();

    public record Order(
            String orderId,
            String description,
            double amountUsd,
            int daysSinceCharge,
            int outageHours) {
    }

    public record RecordedRefund(String refundId, String orderId, double amountUsd, String idempotencyKey) {
    }

    /** The payment provider did not answer in time; a retry may succeed. */
    public static class PaymentTimeoutException extends RuntimeException {
        public PaymentTimeoutException(final String message) {
            super(message);
        }
    }

    /** The idempotency key already belongs to a refund of a different order or amount. */
    public static class IdempotencyKeyConflictException extends RuntimeException {
        public IdempotencyKeyConflictException(final String message) {
            super(message);
        }
    }

    public FakeBillingService(final Path ledgerPath) {
        this.ledger = new LockedJsonLedger<>(ledgerPath, RecordedRefund.class);
    }

    public Order getOrder(final String orderId) {
        final Order order = ORDERS.get(orderId);
        if (order == null) {
            throw new IllegalArgumentException("unknown order " + orderId);
        }
        return order;
    }

    public String refund(
            final Order order, final String idempotencyKey, final boolean simulateTimeout) {
        if (simulateTimeout) {
            simulatedTimeouts.incrementAndGet();
            throw new PaymentTimeoutException(
                    "no answer from the payment provider for " + order.orderId());
        }
        // The key check, the new refund ID, and its record commit under one lock.
        final RefundAttempt attempt = ledger.changeRecords(refunds -> {
            for (RecordedRefund earlier : refunds) {
                if (earlier.idempotencyKey().equals(idempotencyKey)) {
                    if (!earlier.orderId().equals(order.orderId())
                            || Double.compare(earlier.amountUsd(), order.amountUsd()) != 0) {
                        throw new IdempotencyKeyConflictException(String.format(Locale.US,
                                "key %s already refunded %s for %s USD, not %s for %s USD;"
                                        + " give each Dex environment its own ledger directory",
                                idempotencyKey,
                                earlier.orderId(),
                                formatUsd(earlier.amountUsd()),
                                order.orderId(),
                                formatUsd(order.amountUsd())));
                    }
                    return new LockedJsonLedger.Change<>(null, new RefundAttempt(earlier, false));
                }
            }
            final RecordedRefund refund = new RecordedRefund(
                    "rf_" + (1042 + refunds.size()), order.orderId(), order.amountUsd(), idempotencyKey);
            refunds.add(refund);
            return new LockedJsonLedger.Change<>(refunds, new RefundAttempt(refund, true));
        });
        final RecordedRefund refund = attempt.refund();
        if (attempt.newlyIssued()) {
            System.out.printf("Refunded %s USD as %s%n", formatUsd(refund.amountUsd()), refund.refundId());
        } else {
            System.out.printf("Refund %s already issued (key %s)%n", refund.refundId(), idempotencyKey);
        }
        return refund.refundId();
    }

    private record RefundAttempt(RecordedRefund refund, boolean newlyIssued) {
    }

    public List<RecordedRefund> listRefunds() {
        return ledger.readRecords();
    }

    /** Returns how many refund calls this instance failed with a simulated timeout. */
    public int countSimulatedTimeouts() {
        return simulatedTimeouts.get();
    }

    public Path ledgerPath() {
        return ledger.path();
    }

    public static String formatUsd(final double amountUsd) {
        return String.format(Locale.US, "%,.2f", amountUsd);
    }

    /** Stands in for a release whose Issue refund Step forgot the billing call. */
    public static class CallMissingBillingService extends FakeBillingService {
        public CallMissingBillingService(final Path ledgerPath) {
            super(ledgerPath);
        }

        @Override
        public String refund(final Order order, final String idempotencyKey, final boolean simulateTimeout) {
            System.out.printf("Billing call missing: no refund for %s%n", order.orderId());
            return "";
        }
    }
}
