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

package io.superdurable.dex.patterns.failfast;

import org.springframework.stereotype.Component;

import java.util.HashMap;
import java.util.Map;

/**
 * Stands in for a payment provider API.
 * It rejects a bank account that is not eight digits at once.
 * It times out on the first submission of every valid claim, then pays it.
 */
@Component
public class FakePaymentProvider {
    private final Map<String, Integer> submissions = new HashMap<>();

    /** The provider confirmed that the request can never succeed. */
    public static final class PayoutRejectedException extends RuntimeException {
        public PayoutRejectedException(final String reason) {
            super(reason);
        }
    }

    /** The provider did not answer in time; the same request may succeed later. */
    public static final class PayoutTimeoutException extends RuntimeException {
        public PayoutTimeoutException(final String message) {
            super(message);
        }
    }

    /** Pays a claim once per claim ID, so a retried Step does not pay twice. */
    public String submitPayout(final String claimId, final ExpenseClaim claim) {
        final int count;
        synchronized (submissions) {
            count = submissions.merge(claimId, 1, Integer::sum);
        }
        if (!claim.bankAccount().matches("\\d{8}")) {
            throw new PayoutRejectedException("bank account " + claim.bankAccount() + " is invalid");
        }
        if (count == 1) {
            throw new PayoutTimeoutException("payment provider timed out");
        }
        return "payout-" + claimId;
    }

    public int countSubmissions(final String claimId) {
        synchronized (submissions) {
            return submissions.getOrDefault(claimId, 0);
        }
    }
}
