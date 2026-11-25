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

import io.superdurable.dex.Client;
import io.superdurable.dex.patterns.failfast.ExpenseReimbursementFlow.PayoutOutcome;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/fail-fast")
public class FailFastController {
    private final Client client;
    private final ExpenseReimbursementFlow expenseReimbursementFlow;

    public FailFastController(
            final Client client,
            final ExpenseReimbursementFlow expenseReimbursementFlow) {
        this.client = client;
        this.expenseReimbursementFlow = expenseReimbursementFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startExpenseReimbursement(
            @RequestParam final String workflowId,
            @RequestParam final long amountCents,
            @RequestParam final String bankAccount) {
        final String runId = client.startFlow(
                expenseReimbursementFlow,
                workflowId,
                new ExpenseClaim(amountCents, bankAccount),
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/get-outcome")
    ResponseEntity<PayoutOutcome> getPayoutOutcome(@RequestParam final String workflowId) {
        return client.getAttributes(workflowId, ExpenseReimbursementFlow.payoutOutcome)
                .get(ExpenseReimbursementFlow.payoutOutcome)
                .map(ResponseEntity::ok)
                .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
