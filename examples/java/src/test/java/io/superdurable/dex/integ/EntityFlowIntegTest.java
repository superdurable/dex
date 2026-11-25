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

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.superdurable.dex.AttributeValues;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.patterns.entityflow.CustomerAccountFlow;
import io.superdurable.dex.patterns.entityflow.CustomerAccountFlow.ContactDetails;
import io.superdurable.dex.patterns.entityflow.CustomerAccountFlow.CreditAdjustment;
import io.superdurable.dex.patterns.entityflow.FakeAccountArchive;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

@ExtendWith(SharedIntegExtension.class)
public class EntityFlowIntegTest {
    private static final int CONCURRENT_DEPOSITS = 8;

    @Test
    void entityFlowKeepsOneCustomerAccountUntilRetired() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final Client client = environment.client();
        final CustomerAccountFlow flow = environment.customerAccountFlow();
        final String flowId = CustomerAccountFlow.customerAccountFlowId(
                environment.newFlowId("entity-flow"));
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .idReusePolicy(IdReusePolicy.DISALLOW)
                .addAttribute(flow.displayName, "Ada")
                .addAttribute(flow.email, "ada@example.com")
                .addAttribute(flow.creditBalance, 0L)
                .addAttribute(flow.retired, false)
                .build();
        client.startFlow(flow, flowId, null, options);
        assertThrows(
                FlowAlreadyStartedException.class,
                () -> client.startFlow(flow, flowId, null, options));

        final CustomerAccountFlow stub = client.newRpcStub(CustomerAccountFlow.class, flowId);
        client.invokeRPC(
                stub::updateContactDetails,
                new ContactDetails("Ada Lovelace", "ada@lovelace.example"));
        final RuntimeException invalidEmail = assertThrows(
                RuntimeException.class,
                () -> client.invokeRPC(
                        stub::updateContactDetails,
                        new ContactDetails("Ada", "not-an-email")));
        assertTrue(invalidEmail.getMessage().contains("email must contain @"));

        final List<CompletableFuture<Void>> deposits = new ArrayList<>();
        for (int deposit = 0; deposit < CONCURRENT_DEPOSITS; deposit++) {
            deposits.add(CompletableFuture.runAsync(() -> depositWithRetry(client, flowId, 5)));
        }
        CompletableFuture.allOf(deposits.toArray(new CompletableFuture<?>[0])).join();
        final RuntimeException overdraft = assertThrows(
                RuntimeException.class,
                () -> client.invokeRPC(stub::adjustCreditBalance, new CreditAdjustment(-1000)));
        assertTrue(overdraft.getMessage().contains("below zero"));

        final AttributeValues values = client.getAttributes(
                flowId, flow.displayName, flow.email, flow.creditBalance);
        assertEquals("Ada Lovelace", values.get(flow.displayName).orElseThrow());
        assertEquals("ada@lovelace.example", values.get(flow.email).orElseThrow());
        assertEquals(5L * CONCURRENT_DEPOSITS, values.get(flow.creditBalance).orElseThrow());

        final RuntimeException retireWithBalance = assertThrows(
                RuntimeException.class,
                () -> client.invokeRPC(stub::retireAccount));
        assertTrue(retireWithBalance.getMessage().contains("before retiring"));
        assertEquals(FlowStatus.RUNNING, client.describeFlow(flowId).getStatus());

        client.invokeRPC(
                stub::adjustCreditBalance,
                new CreditAdjustment(-5L * CONCURRENT_DEPOSITS));
        final FakeAccountArchive archive = environment.accountArchive();
        archive.makeUnavailable(flowId);
        client.invokeRPC(stub::retireAccount);
        assertEquals(FlowStatus.RUNNING, client.describeFlow(flowId).getStatus());
        final RuntimeException retiredAdjustment = assertThrows(
                RuntimeException.class,
                () -> client.invokeRPC(stub::adjustCreditBalance, new CreditAdjustment(10)));
        assertTrue(retiredAdjustment.getMessage().contains("account is retired"));
        final RuntimeException retiredContact = assertThrows(
                RuntimeException.class,
                () -> client.invokeRPC(
                        stub::updateContactDetails,
                        new ContactDetails("Ada Byron", "ada@byron.example")));
        assertTrue(retiredContact.getMessage().contains("account is retired"));
        client.invokeRPC(stub::retireAccount);
        assertEquals(FlowStatus.RUNNING, client.describeFlow(flowId).getStatus());

        archive.makeAvailable(flowId);
        assertEquals(
                FlowStatus.COMPLETED,
                client.waitForFlow(flowId, Duration.ofSeconds(45)).getStatus());
        assertEquals(1, archive.archiveCount(flowId));
        final AttributeValues closed = client.getAttributes(flowId, flow.creditBalance, flow.retired);
        assertEquals(0L, closed.get(flow.creditBalance).orElseThrow());
        assertEquals(Boolean.TRUE, closed.get(flow.retired).orElseThrow());

        assertThrows(
                FlowAlreadyStartedException.class,
                () -> client.startFlow(flow, flowId, null, options));
    }

    private static void depositWithRetry(
            final Client client,
            final String flowId,
            final long amount) {
        final CustomerAccountFlow stub = client.newRpcStub(CustomerAccountFlow.class, flowId);
        for (int attempt = 1; attempt <= 50; attempt++) {
            try {
                client.invokeRPC(stub::adjustCreditBalance, new CreditAdjustment(amount));
                return;
            } catch (final RpcLockConflictException conflict) {
                try {
                    Thread.sleep(attempt * 50L);
                } catch (final InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    throw new IllegalStateException(interrupted);
                }
            }
        }
        throw new IllegalStateException("credit deposit kept conflicting on the balance lock");
    }
}
