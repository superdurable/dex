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

import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

import java.nio.file.Path;

/** Registers the customer refund Flow in the shared example application. */
@Configuration
public class CustomerRefundConfig {

    /** Returns the Flow with normal billing and the ledgers in ledgerDirectory. */
    @Bean
    public CustomerRefundFlow customerRefundFlow(
            @Value("${dex.customer-refund-ledger-dir}") final String ledgerDirectory) {
        return newSharedRefundFlow(Path.of(ledgerDirectory));
    }

    /** Returns the normal-billing Flow that keeps its ledgers in ledgerDirectory. */
    public static CustomerRefundFlow newSharedRefundFlow(final Path ledgerDirectory) {
        return RefundSettings.newRefundFlow(
                RefundSettings.NORMAL_BILLING,
                billingLedger(ledgerDirectory),
                emailLedger(ledgerDirectory));
    }

    /** Returns the billing ledger file inside ledgerDirectory. */
    public static Path billingLedger(final Path ledgerDirectory) {
        return ledgerDirectory.resolve(FakeBillingService.DEFAULT_BILLING_LEDGER.getFileName());
    }

    /** Returns the email ledger file inside ledgerDirectory. */
    public static Path emailLedger(final Path ledgerDirectory) {
        return ledgerDirectory.resolve(FakeEmailService.DEFAULT_EMAIL_LEDGER.getFileName());
    }
}
