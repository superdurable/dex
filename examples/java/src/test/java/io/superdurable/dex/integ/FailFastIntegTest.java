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

package io.superdurable.dex.integ;

import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.patterns.failfast.ExpenseClaim;
import io.superdurable.dex.patterns.failfast.ExpenseReimbursementFlow;
import io.superdurable.dex.patterns.failfast.ExpenseReimbursementFlow.PayoutOutcome;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;

@ExtendWith(SharedIntegExtension.class)
public class FailFastIntegTest {
    @Test
    void failFastRoutesRejectedBankAccountAfterOneAttempt() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId("pattern-fail-fast");
        environment.client().startFlow(
                environment.expenseReimbursementFlow(),
                flowId,
                new ExpenseClaim(12_000, "not-a-bank-account"),
                environment.startOptions());

        final FlowResult result = environment.client().waitForFlow(flowId, Duration.ofSeconds(45));
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(
                "rejected: bank account not-a-bank-account is invalid",
                result.getSingleOutput(String.class));
        assertEquals(
                new PayoutOutcome("REJECTED", "bank account not-a-bank-account is invalid", 1),
                readPayoutOutcome(environment, flowId));
        assertEquals(1, environment.fakePaymentProvider().countSubmissions(flowId));
    }

    @Test
    void failFastRetriesProviderTimeout() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId("pattern-fail-fast");
        environment.client().startFlow(
                environment.expenseReimbursementFlow(),
                flowId,
                new ExpenseClaim(12_000, "12345678"),
                environment.startOptions());

        final FlowResult result = environment.client().waitForFlow(flowId, Duration.ofSeconds(45));
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals("payout-" + flowId, result.getSingleOutput(String.class));
        assertEquals(
                new PayoutOutcome("PAID", "payout-" + flowId, 2),
                readPayoutOutcome(environment, flowId));
        assertEquals(2, environment.fakePaymentProvider().countSubmissions(flowId));
    }

    private static PayoutOutcome readPayoutOutcome(
            final IntegEnvironment environment,
            final String flowId) {
        return environment.client()
                .getAttributes(flowId, ExpenseReimbursementFlow.payoutOutcome)
                .get(ExpenseReimbursementFlow.payoutOutcome)
                .orElseThrow();
    }
}
