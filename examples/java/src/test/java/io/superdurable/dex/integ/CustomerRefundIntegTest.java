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

import io.superdurable.dex.AttributeMatch;
import io.superdurable.dex.BlobCache;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.Registry;
import io.superdurable.dex.WaitForAttributeOptions;
import io.superdurable.dex.Worker;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.DecisionReason;
import io.superdurable.dex.products.customerrefund.FakeBillingService;
import io.superdurable.dex.products.customerrefund.FakeBillingService.RecordedRefund;
import io.superdurable.dex.products.customerrefund.FakeEmailService;
import io.superdurable.dex.products.customerrefund.MockMessageModel;
import io.superdurable.dex.products.customerrefund.RefundSettings;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.TimeoutException;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

/** Runs the customer refund example's scenarios against a real Dex server. */
public class CustomerRefundIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    private static final String SERVER_ADDRESS =
            System.getenv().getOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801");

    @TempDir
    Path directory;

    private FakeBillingService billing;
    private FakeEmailService email;
    private CustomerRefundFlow flow;
    private BlobCache clientCache;
    private Client client;

    @BeforeEach
    void openClient() {
        billing = new FakeBillingService(directory.resolve("billing-refunds.json"));
        email = new FakeEmailService(directory.resolve("sent-emails.json"));
        flow = newFlow("normal");
        clientCache = RefundSettings.openCache(directory.resolve("client-cache"));
        client = new Client(new Registry(List.of(flow)), clientCache, new ClientOptions(SERVER_ADDRESS));
    }

    @AfterEach
    void closeClient() {
        client.close();
        clientCache.close();
    }

    @Test
    void managerApprovesTheNorthstarRefund() throws Exception {
        final String flowId = newFlowId();
        final FlowResult result;
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("normal"));
            waitForManager(flowId);
            client.invokeRPC(stub(flowId)::approveRefund, new DecisionReason("Outage confirmed"));
            result = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }

        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals("refunded", result.getSingleOutput(String.class));
        final List<RecordedRefund> refunds = billing.listRefunds();
        assertEquals(1, refunds.size());
        assertEquals("INV-1249", refunds.get(0).orderId());
        assertEquals(1249.00, refunds.get(0).amountUsd());
        assertEquals(refunds.get(0).refundId(), readAttribute(flowId, CustomerRefundFlow.REFUND_ID));
        assertEquals(List.of("email-" + flowId), email.listSentKeys());
    }

    @Test
    void managerRejectsTheRefund() throws Exception {
        final String flowId = newFlowId();
        final FlowResult result;
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("normal"));
            waitForManager(flowId);
            client.invokeRPC(stub(flowId)::rejectRefund, new DecisionReason("Credit offered instead"));
            result = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }

        assertEquals("denied", result.getSingleOutput(String.class));
        assertEquals(List.of(), billing.listRefunds());
        assertTrue(readAttribute(flowId, CustomerRefundFlow.CUSTOMER_MESSAGE).contains("Credit offered instead"));
    }

    @Test
    void policyDeniesAnOldCharge() throws Exception {
        final String flowId = newFlowId();
        final FlowResult result;
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("deny"));
            result = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }

        assertEquals("denied", result.getSingleOutput(String.class));
        assertEquals("closed-denied", readAttribute(flowId, CustomerRefundFlow.CASE_STATUS));
        assertEquals(List.of(), billing.listRefunds());
        assertEquals(List.of("email-" + flowId), email.listSentKeys());
    }

    @Test
    void refundSucceedsAfterPaymentTimeouts() throws Exception {
        final String flowId = newFlowId();
        final FakeBillingService workerBilling = new FakeBillingService(billing.ledgerPath());
        final CustomerRefundFlow workerFlow = new CustomerRefundFlow(workerBilling, email, new MockMessageModel());
        final FlowResult result;
        try (RunningWorker ignored = startWorker(workerFlow)) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("fail-then-succeed"));
            waitForManager(flowId);
            client.invokeRPC(stub(flowId)::approveRefund, new DecisionReason("Outage confirmed"));
            result = client.waitForFlow(flowId, Duration.ofSeconds(90));
        }

        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(2, workerBilling.countSimulatedTimeouts());
        assertEquals(1, billing.listRefunds().size());
    }

    @Test
    void timeTravelIssuesTheRefundAMissingCallSkipped() throws Exception {
        final String flowId = newFlowId();
        final FlowResult broken;
        try (RunningWorker ignored = startWorker(newFlow("call-missing"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("normal"));
            waitForManager(flowId);
            client.invokeRPC(stub(flowId)::approveRefund, new DecisionReason("Outage confirmed"));
            broken = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }
        assertEquals("refunded", broken.getSingleOutput(String.class));
        assertEquals("", readAttribute(flowId, CustomerRefundFlow.REFUND_ID));
        assertEquals(List.of(), billing.listRefunds());

        final FlowResult fixed;
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            timeTravelWithDexcli(flowId, "IssueRefund");
            fixed = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }

        assertEquals(FlowStatus.COMPLETED, fixed.getStatus());
        final List<RecordedRefund> refunds = billing.listRefunds();
        assertEquals(List.of("refund-" + flowId), refunds.stream().map(RecordedRefund::idempotencyKey).toList());
        assertEquals(refunds.get(0).refundId(), readAttribute(flowId, CustomerRefundFlow.REFUND_ID));
        assertEquals(List.of("email-" + flowId), email.listSentKeys());
    }

    private CustomerRefundFlow newFlow(final String billingMode) {
        return RefundSettings.newRefundFlow(billingMode, billing.ledgerPath(), email.ledgerPath());
    }

    private CustomerRefundFlow stub(final String flowId) {
        return client.newRpcStub(CustomerRefundFlow.class, flowId);
    }

    private void waitForManager(final String flowId) throws InterruptedException {
        client.waitForAttributeMatch(
                flowId,
                CustomerRefundFlow.CASE_STATUS,
                AttributeMatch.equalTo(CustomerRefundFlow.AWAITING_MANAGER),
                WaitForAttributeOptions.newBuilder().requestTimeout(WAIT_TIMEOUT).build());
    }

    private String readAttribute(
            final String flowId, final io.superdurable.dex.Attribute<String> attribute) {
        return client.getAttributes(flowId, attribute).get(attribute).orElse(null);
    }

    private RunningWorker startWorker(final CustomerRefundFlow workerFlow)
            throws InterruptedException, TimeoutException {
        final BlobCache cache = RefundSettings.openCache(directory.resolve("worker-cache-" + System.nanoTime()));
        final Worker worker = new Worker(
                new Registry(List.of(workerFlow)),
                cache,
                RefundSettings.refundWorkerOptions(SERVER_ADDRESS, RefundSettings.newWorkerId()));
        final Thread thread = new Thread(worker::start, "customer-refund-integ-worker");
        thread.setDaemon(true);
        thread.start();
        final RunningWorker running = new RunningWorker(worker, thread, cache);
        try {
            worker.waitPullRegistered(Duration.ofSeconds(30));
        } catch (final InterruptedException | TimeoutException failure) {
            running.close();
            throw failure;
        }
        return running;
    }

    private static String newFlowId() {
        return "refund-" + System.nanoTime();
    }

    /** Runs the README's time travel command. */
    private static void timeTravelWithDexcli(final String flowId, final String stepType)
            throws IOException, InterruptedException {
        final String dexcli = System.getenv().getOrDefault("DEXCLI_PATH", "dexcli");
        final Process process = new ProcessBuilder(
                dexcli, "flow", "time-travel", flowId,
                "--server", SERVER_ADDRESS,
                "--type", "step-type",
                "--target", stepType,
                "--reason", "billing call restored",
                "--yes")
                .redirectErrorStream(true)
                .start();
        final String output = new String(process.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
        if (process.waitFor() != 0) {
            fail("dexcli flow time-travel failed: " + output);
        }
    }

    private record RunningWorker(Worker worker, Thread thread, BlobCache cache) implements AutoCloseable {
        @Override
        public void close() throws InterruptedException {
            worker.close();
            thread.join(10_000L);
            cache.close();
        }
    }
}
