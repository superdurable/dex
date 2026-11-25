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

package io.superdurable.dex.patterns.entityflow;

import io.superdurable.dex.AttributeValues;
import io.superdurable.dex.Client;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import java.util.Map;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/entity-flow")
public class EntityFlowController {
    private final Client client;
    private final CustomerAccountFlow flow;

    public EntityFlowController(final Client client, final CustomerAccountFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @PostMapping("/account")
    ResponseEntity<Map<String, String>> openAccount(@RequestBody final AccountRequest request) {
        final String flowId = CustomerAccountFlow.customerAccountFlowId(request.requireAccountId());
        final CustomerAccountFlow.ContactDetails contact = request.contactDetails();
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .idReusePolicy(IdReusePolicy.DISALLOW)
                .addAttribute(flow.displayName, contact.displayName)
                .addAttribute(flow.email, contact.email)
                .addAttribute(flow.creditBalance, 0L)
                .addAttribute(flow.retired, false)
                .build();
        try {
            final String runId = client.startFlow(flow, flowId, null, options);
            return ResponseEntity.ok(Map.of("flowID", flowId, "runID", runId));
        } catch (final FlowAlreadyStartedException alreadyStarted) {
            return ResponseEntity.status(HttpStatus.CONFLICT)
                    .body(Map.of("error", "account already exists"));
        }
    }

    @PostMapping("/account/contact")
    ResponseEntity<String> updateContactDetails(@RequestBody final AccountRequest request) {
        final CustomerAccountFlow stub = accountStub(request.requireAccountId());
        client.invokeRPC(stub::updateContactDetails, request.contactDetails());
        return ResponseEntity.ok("Updated contact details");
    }

    @PostMapping("/account/credits")
    ResponseEntity<Map<String, Long>> adjustCreditBalance(
            @RequestBody final CreditAdjustmentRequest request) {
        final CustomerAccountFlow stub = accountStub(requireAccountId(request.accountId));
        final Long balance = client.invokeRPC(
                stub::adjustCreditBalance,
                new CustomerAccountFlow.CreditAdjustment(request.amount));
        return ResponseEntity.ok(Map.of("creditBalance", balance));
    }

    @GetMapping("/account")
    ResponseEntity<Map<String, Object>> getAccount(@RequestParam final String accountId) {
        final AttributeValues values = client.getAttributes(
                CustomerAccountFlow.customerAccountFlowId(requireAccountId(accountId)),
                flow.displayName,
                flow.email,
                flow.creditBalance);
        return ResponseEntity.ok(Map.of(
                "accountId", accountId,
                "displayName", values.get(flow.displayName).orElse(""),
                "email", values.get(flow.email).orElse(""),
                "creditBalance", values.get(flow.creditBalance).orElse(0L)));
    }

    @PostMapping("/account/retire")
    ResponseEntity<String> retireAccount(@RequestParam final String accountId) {
        final CustomerAccountFlow stub = accountStub(requireAccountId(accountId));
        client.invokeRPC(stub::retireAccount);
        return ResponseEntity.ok("Retired account");
    }

    private CustomerAccountFlow accountStub(final String accountId) {
        return client.newRpcStub(
                CustomerAccountFlow.class,
                CustomerAccountFlow.customerAccountFlowId(accountId));
    }

    private static String requireAccountId(final String accountId) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("accountId is required");
        }
        return accountId;
    }

    public static class AccountRequest {
        public String accountId;
        public String displayName;
        public String email;

        String requireAccountId() {
            return EntityFlowController.requireAccountId(accountId);
        }

        CustomerAccountFlow.ContactDetails contactDetails() {
            final CustomerAccountFlow.ContactDetails contact =
                    new CustomerAccountFlow.ContactDetails(displayName, email);
            contact.validate();
            return contact;
        }
    }

    public static class CreditAdjustmentRequest {
        public String accountId;
        public long amount;
    }
}
