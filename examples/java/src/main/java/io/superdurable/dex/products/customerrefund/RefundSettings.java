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

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;
import io.superdurable.dex.PullWorkerOptions;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.RefundRequest;
import io.superdurable.dex.products.customerrefund.FakeBillingService.CallMissingBillingService;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.SecureRandom;
import java.util.Collections;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.Map;

/** Settings shared by the refund commands and the tests. */
public final class RefundSettings {
    public static final String SERVER_ADDRESS =
            System.getenv().getOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801");
    public static final String BILLING_MODE =
            System.getenv().getOrDefault("REFUND_BILLING_MODE", "normal");

    private static final RefundRequest NORTHSTAR_RENEWAL = new RefundRequest(
            "Northstar Studio",
            "billing@northstar.example",
            "INV-1249",
            "Our workspace was down for 31 hours after the renewal.",
            0);

    /** The start command's scenarios, by name; the first is the default. */
    public static final Map<String, RefundRequest> SCENARIOS = scenarios();

    private RefundSettings() {
    }

    /** Returns the Flow; billing mode "call-missing" leaves out the billing call. */
    public static CustomerRefundFlow newRefundFlow(
            final String billingMode, final Path billingLedger, final Path emailLedger) {
        final FakeBillingService billing = switch (billingMode) {
            case "normal" -> new FakeBillingService(billingLedger);
            case "call-missing" -> new CallMissingBillingService(billingLedger);
            default -> throw new IllegalArgumentException(
                    "REFUND_BILLING_MODE must be normal or call-missing, not " + billingMode);
        };
        return new CustomerRefundFlow(billing, new FakeEmailService(emailLedger), new MockMessageModel());
    }

    /** Returns a Worker ID without the host name; it changes on every start. */
    public static String newWorkerId() {
        final byte[] suffix = new byte[3];
        new SecureRandom().nextBytes(suffix);
        return "refund-worker-" + HexFormat.of().formatHex(suffix);
    }

    /** Returns Worker options for the refund Worker. */
    public static WorkerOptions refundWorkerOptions(final String serverAddress, final String workerId) {
        return WorkerOptions.newBuilder()
                .serverAddress(serverAddress)
                .pull(PullWorkerOptions.newBuilder().appWorkerID(workerId).build())
                .build();
    }

    /** Opens a local cache for large Flow values, up to 1 GiB. */
    public static BlobCache openCache(final Path directory) {
        try {
            Files.createDirectories(directory);
        } catch (final IOException failure) {
            throw new UncheckedIOException(failure);
        }
        return BlobCache.open(new BlobCacheConfig(directory.toString(), 1L << 30));
    }

    private static Map<String, RefundRequest> scenarios() {
        final Map<String, RefundRequest> scenarios = new LinkedHashMap<>();
        scenarios.put("normal", NORTHSTAR_RENEWAL);
        scenarios.put("fail-then-succeed", NORTHSTAR_RENEWAL.withPaymentTimeoutsToSimulate(2));
        scenarios.put("deny", NORTHSTAR_RENEWAL.withOrderId("INV-0388"));
        scenarios.put("small", NORTHSTAR_RENEWAL.withOrderId("INV-0049"));
        return Collections.unmodifiableMap(scenarios);
    }
}
