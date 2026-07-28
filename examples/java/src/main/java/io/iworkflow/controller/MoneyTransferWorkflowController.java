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

package io.iworkflow.controller;

import io.iworkflow.core.Client;
import io.iworkflow.core.WorkflowUncompletedException;
import io.iworkflow.core.exceptions.LongPollTimeoutException;
import io.iworkflow.workflow.money.transfer.ImmutableTransferRequest;
import io.iworkflow.workflow.money.transfer.MoneyTransferWorkflow;
import io.iworkflow.workflow.money.transfer.TransferRequest;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

@Controller
@RequestMapping("/moneytransfer")
public class MoneyTransferWorkflowController {

    private final Client client;

    public MoneyTransferWorkflowController(
            final Client client
    ) {
        this.client = client;
    }

    @GetMapping("/start")
    public ResponseEntity<String> start(
            @RequestParam String fromAccount,
            @RequestParam String toAccount,
            @RequestParam int amount,
            @RequestParam(defaultValue = "none") String notes
    ) {
        String workflowId = "money_transfer_" + System.currentTimeMillis() / 1000;
        TransferRequest request = ImmutableTransferRequest.builder()
                .fromAccountId(fromAccount)
                .toAccountId(toAccount)
                .amount(amount)
                .notes(notes)
                .build();

        try {
            client.startWorkflow(MoneyTransferWorkflow.class, workflowId, 3600, request);
            String message = client.getSimpleWorkflowResultWithWait(String.class, workflowId);
            return ResponseEntity.ok(message);
        } catch (LongPollTimeoutException | WorkflowUncompletedException e) {
            throw e;
        } catch (Exception e) {
            return ResponseEntity.ok("failed " + e.getMessage());
        }
    }
}