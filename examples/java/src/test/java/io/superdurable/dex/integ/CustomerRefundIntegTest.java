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

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.AttributeMatch;
import io.superdurable.dex.BlobCache;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.Registry;
import io.superdurable.dex.WaitForAttributeOptions;
import io.superdurable.dex.Worker;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
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
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

/** Runs the customer refund example's scenarios against a real Dex server. */
public class CustomerRefundIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    private static final ObjectMapper JSON = new ObjectMapper();
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
        assertTrue(readAttribute(flowId, CustomerRefundFlow.CUSTOMER_MESSAGE)
                .endsWith("The charge is outside the 30-day window and no outage was recorded."));
        assertEquals(List.of(), billing.listRefunds());
        assertEquals(List.of("email-" + flowId), email.listSentKeys());
    }

    @Test
    void policyNamesOnlyTheExpiredWindow() throws Exception {
        assertEquals("The charge is outside the 30-day window.", denialNoteFor("INV-0412"));
    }

    @Test
    void policyNamesOnlyTheMissingOutage() throws Exception {
        assertEquals("No outage was recorded for this order.", denialNoteFor("INV-0517"));
    }

    @Test
    void blankReasonIsRejectedAndAValidDecisionStillWorks() throws Exception {
        final String flowId = newFlowId();
        final FlowResult result;
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("normal"));
            waitForManager(flowId);
            assertBlankReasonRejected(assertThrows(WorkerInvocationException.class, () ->
                    client.invokeRPC(stub(flowId)::approveRefund, new DecisionReason("   "))));
            assertBlankReasonRejected(assertThrows(WorkerInvocationException.class, () ->
                    client.invokeRPC(stub(flowId)::rejectRefund, new DecisionReason(null))));
            assertEquals(CustomerRefundFlow.AWAITING_MANAGER, readAttribute(flowId, CustomerRefundFlow.CASE_STATUS));

            client.invokeRPC(stub(flowId)::approveRefund, new DecisionReason("Outage confirmed"));
            result = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }

        assertEquals("refunded", result.getSingleOutput(String.class));
        assertEquals(1, billing.listRefunds().size());
        final JsonNode events = readHistory(flowId);
        assertEquals(1, countEvents(events, "RpcExecutionCompleted"));
        assertEquals(List.of("Outage confirmed"), managerDecisionReasons(events));
    }

    @Test
    void concurrentApproveAndRejectLetOnlyOneDecisionWin() throws Exception {
        final String flowId = newFlowId();
        final FlowResult result;
        final List<String> winners = new ArrayList<>();
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("normal"));
            waitForManager(flowId);
            final CustomerRefundFlow stub = stub(flowId);
            final ExecutorService executor = Executors.newFixedThreadPool(2);
            final CountDownLatch startGate = new CountDownLatch(1);
            try {
                final Future<String> approve = executor.submit(() -> {
                    startGate.await();
                    client.invokeRPC(stub::approveRefund, new DecisionReason("Outage confirmed"));
                    return "approve";
                });
                final Future<String> reject = executor.submit(() -> {
                    startGate.await();
                    client.invokeRPC(stub::rejectRefund, new DecisionReason("Credit offered instead"));
                    return "reject";
                });
                startGate.countDown();
                for (Future<String> decision : List.of(approve, reject)) {
                    try {
                        winners.add(decision.get(30, TimeUnit.SECONDS));
                    } catch (final ExecutionException lost) {
                        assertTrue(lost.getCause() instanceof DexServiceException, lost.getCause().toString());
                    }
                }
            } finally {
                executor.shutdownNow();
            }
            result = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }

        assertEquals(1, winners.size(), "winners " + winners);
        final boolean approved = winners.get(0).equals("approve");
        assertEquals(approved ? "refunded" : "denied", result.getSingleOutput(String.class));
        assertEquals(approved ? 1 : 0, billing.listRefunds().size());
        final JsonNode events = readHistory(flowId);
        assertEquals(1, countEvents(events, "RpcExecutionCompleted"));
        assertEquals(1, managerDecisionReasons(events).size());
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

        final JsonNode issueRefund = issueRefundCompletedContext(flowId);
        assertEquals(3, issueRefund.path("finalAttempt").asInt());
        final JsonNode lastFailure = issueRefund.path("lastFailureInfo");
        assertEquals(2, lastFailure.path("attempt").asInt());
        assertTrue(lastFailure.path("details").path("originalWorkerErrorType").asText()
                .endsWith("PaymentTimeoutException"), lastFailure.toString());
        assertTrue(lastFailure.path("details").path("originalWorkerErrorStackTrace").asText()
                .contains("FakeBillingService.refund"), lastFailure.toString());
    }

    @Test
    void timeTravelToASucceededRefundReusesItsRefundId() throws Exception {
        final String flowId = newFlowId();
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("small"));
            client.waitForFlow(flowId, WAIT_TIMEOUT);
            final String firstRefundId = readAttribute(flowId, CustomerRefundFlow.REFUND_ID);

            timeTravelWithDexcli(flowId, "IssueRefund");
            final JsonNode events = awaitClosedRunAttempt(flowId, 2);
            assertTrue(stepCompletedIn(events, "IssueRefund", 2), "IssueRefund did not run again");
            assertEquals(firstRefundId, readAttribute(flowId, CustomerRefundFlow.REFUND_ID));
        }

        final List<RecordedRefund> refunds = billing.listRefunds();
        assertEquals(1, refunds.size());
        assertEquals(readAttribute(flowId, CustomerRefundFlow.REFUND_ID), refunds.get(0).refundId());
        assertEquals(List.of("email-" + flowId), email.listSentKeys());
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
            awaitClosedRunAttempt(flowId, 2);
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

    private String denialNoteFor(final String orderId) throws Exception {
        final String flowId = newFlowId();
        final FlowResult result;
        try (RunningWorker ignored = startWorker(newFlow("normal"))) {
            client.startFlow(flow, flowId, RefundSettings.SCENARIOS.get("normal").withOrderId(orderId));
            result = client.waitForFlow(flowId, WAIT_TIMEOUT);
        }
        assertEquals("denied", result.getSingleOutput(String.class));
        assertEquals(List.of(), billing.listRefunds());
        final String message = readAttribute(flowId, CustomerRefundFlow.CUSTOMER_MESSAGE);
        final String greeting = "We cannot refund this charge. ";
        return message.substring(message.indexOf(greeting) + greeting.length());
    }

    private static void assertBlankReasonRejected(final WorkerInvocationException rejected) {
        assertTrue(rejected.getWorkerErrorType().endsWith("IllegalArgumentException"), rejected.toString());
        assertEquals("a decision needs a reason", rejected.getWorkerErrorDetail());
    }

    private static long countEvents(final JsonNode events, final String type) {
        long count = 0;
        for (JsonNode event : events) {
            if (event.path("type").asText().equals(type)) {
                count++;
            }
        }
        return count;
    }

    /** Returns the reasons of the manager decisions that RequestManagerApproval received. */
    private static List<String> managerDecisionReasons(final JsonNode events) {
        final List<String> reasons = new ArrayList<>();
        for (JsonNode event : events) {
            final JsonNode payload = event.path("payload");
            if (event.path("type").asText().equals("StepExecuteCompleted")
                    && payload.path("context").path("stepType").asText().equals("RequestManagerApproval")) {
                for (JsonNode channel : payload.path("input").path("conditionResults").path("channelResults")) {
                    for (JsonNode decision : channel.path("values")) {
                        reasons.add(decision.path("reason").asText());
                    }
                }
            }
        }
        return reasons;
    }

    /** Returns the context of the IssueRefund StepExecuteCompleted event. */
    private static JsonNode issueRefundCompletedContext(final String flowId) throws Exception {
        for (JsonNode event : readHistory(flowId)) {
            final JsonNode context = event.path("payload").path("context");
            if (event.path("type").asText().equals("StepExecuteCompleted")
                    && context.path("stepType").asText().equals("IssueRefund")) {
                return context;
            }
        }
        throw new AssertionError("no IssueRefund StepExecuteCompleted event in " + flowId);
    }

    /** Polls history until the given run attempt closes, then returns the events. */
    private static JsonNode awaitClosedRunAttempt(final String flowId, final int runAttempt) throws Exception {
        final long deadline = System.nanoTime() + WAIT_TIMEOUT.toNanos();
        while (System.nanoTime() < deadline) {
            final JsonNode events = readHistory(flowId);
            for (JsonNode event : events) {
                if (event.path("type").asText().equals("FlowClosed")
                        && event.path("runAttempt").asInt() == runAttempt) {
                    return events;
                }
            }
            Thread.sleep(200L);
        }
        throw new AssertionError("run attempt " + runAttempt + " of " + flowId + " did not close");
    }

    private static boolean stepCompletedIn(final JsonNode events, final String stepType, final int runAttempt) {
        for (JsonNode event : events) {
            if (event.path("type").asText().equals("StepExecuteCompleted")
                    && event.path("runAttempt").asInt() == runAttempt
                    && event.path("payload").path("context").path("stepType").asText().equals(stepType)) {
                return true;
            }
        }
        return false;
    }

    private static JsonNode readHistory(final String flowId) throws Exception {
        final String output = runDexcli("flow", "history", flowId, "--all", "--server", SERVER_ADDRESS);
        return JSON.readTree(output).path("events");
    }

    /** Runs the README's time travel command. */
    private static void timeTravelWithDexcli(final String flowId, final String stepType)
            throws IOException, InterruptedException {
        runDexcli(
                "flow", "time-travel", flowId,
                "--server", SERVER_ADDRESS,
                "--type", "step-type",
                "--target", stepType,
                "--reason", "billing call restored",
                "--yes");
    }

    private static String runDexcli(final String... args) throws IOException, InterruptedException {
        final List<String> command = new ArrayList<>();
        command.add(System.getenv().getOrDefault("DEXCLI_PATH", "dexcli"));
        command.addAll(List.of(args));
        final Process process = new ProcessBuilder(command)
                .redirectError(ProcessBuilder.Redirect.DISCARD)
                .start();
        final String output = new String(process.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
        if (process.waitFor() != 0) {
            fail("dexcli " + String.join(" ", args) + " failed: " + output);
        }
        return output;
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
