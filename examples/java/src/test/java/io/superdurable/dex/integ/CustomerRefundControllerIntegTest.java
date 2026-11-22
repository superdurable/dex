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

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.products.customerrefund.CustomerRefundConfig;
import io.superdurable.dex.products.customerrefund.CustomerRefundController;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow;
import io.superdurable.dex.products.customerrefund.FakeBillingService;
import io.superdurable.dex.products.customerrefund.FakeBillingService.RecordedRefund;
import io.superdurable.dex.products.customerrefund.FakeEmailService;
import io.superdurable.dex.products.moneytransfer.TransferRequest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.http.ResponseEntity;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Drives the customer refund controller against the shared integ Worker. */
@ExtendWith(SharedIntegExtension.class)
public class CustomerRefundControllerIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    // Mirrors application.yml, which leaves null fields out by default.
    private static final ObjectMapper JSON =
            new ObjectMapper().setSerializationInclusion(JsonInclude.Include.NON_NULL);

    @Test
    void managerApprovesThroughTheController() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);

        // Empty query values count as absent: a new Flow ID and the normal scenario.
        final ResponseEntity<Map<String, String>> started = controller.start("", "", "");
        assertEquals(200, started.getStatusCode().value());
        assertEquals(List.of("flowID", "runID"), List.copyOf(started.getBody().keySet()));
        final String flowId = started.getBody().get("flowID");
        assertTrue(flowId.startsWith("customer-refund-"), flowId);
        assertTrue(!started.getBody().get("runID").isEmpty());
        // start returns after CheckPolicy, so the run already waits for the manager.
        assertDescription(controller.describe(flowId, ""), flowId, "running", "awaiting-manager", null);
        assertDecision(409, "refund " + flowId + " is already started", controller.start(flowId, null, "normal"));

        assertDecision(400, "a decision needs a reason", controller.decide(flowId, null, "approve", "  "));
        assertDecision(400, "decision must be approve or reject", controller.decide(flowId, null, "maybe", "Why"));
        final ResponseEntity<Map<String, String>> decided =
                controller.decide(flowId, "", "approve", "Outage confirmed");
        assertEquals(200, decided.getStatusCode().value());
        assertEquals(Map.of("flowID", flowId, "decision", "approve"), decided.getBody());
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals("refunded", result.getSingleOutput(String.class));

        final RecordedRefund refund = onlyRefundOf(environment, flowId);
        assertDescription(
                controller.describe(null, flowId), flowId, "completed", "closed-refunded", refund.refundId());
        assertTrue(emailKeys(environment).contains("email-" + flowId));
        assertDecision(404, "no running refund has Flow ID " + flowId,
                controller.decide(flowId, null, "reject", "Too late"));
    }

    @Test
    void managerRejectsThroughTheController() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);
        final String flowId = environment.newFlowId("customer-refund");

        assertEquals(flowId, controller.start(flowId, null, "normal").getBody().get("flowID"));
        awaitManager(environment, flowId);
        assertEquals(200,
                controller.decide(null, flowId, "reject", "Credit offered instead").getStatusCode().value());
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);

        assertEquals("denied", result.getSingleOutput(String.class));
        assertDescription(controller.describe(flowId, null), flowId, "completed", "closed-denied", null);
        assertEquals(List.of(), refundsOf(environment, flowId));
    }

    @Test
    void secondDecisionIsAConflictWhileTheRefundRetries() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);
        final String flowId = environment.newFlowId("customer-refund");

        controller.start(null, flowId, "fail-then-succeed");
        awaitManager(environment, flowId);
        assertEquals(200, controller.decide(flowId, null, "approve", "Outage confirmed").getStatusCode().value());
        assertDecision(409, "this refund is not waiting for a manager",
                controller.decide(flowId, null, "reject", "Changed my mind"));
        final FlowResult result = environment.client().waitForFlow(flowId, Duration.ofSeconds(90));

        assertEquals("refunded", result.getSingleOutput(String.class));
        final RecordedRefund refund = onlyRefundOf(environment, flowId);
        assertDescription(
                controller.describe(flowId, null), flowId, "completed", "closed-refunded", refund.refundId());
    }

    @Test
    void describeRejectsAnotherFlowType() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);
        final String transferFlowId = environment.newFlowId("money-transfer");
        environment.client().startFlow(
                environment.moneyTransferFlow(),
                transferFlowId,
                new TransferRequest("from-refund-test", "to-refund-test", 1, "not a refund"),
                environment.startOptions());

        final ResponseEntity<?> described = controller.describe(transferFlowId, null);
        assertEquals(404, described.getStatusCode().value());
        assertEquals(Map.of("error", "no refund has Flow ID " + transferFlowId), described.getBody());
        assertDecision(404, "no running refund has Flow ID " + transferFlowId,
                controller.decide(transferFlowId, null, "approve", "Outage confirmed"));
        environment.client().waitForFlow(transferFlowId, WAIT_TIMEOUT);
    }

    @Test
    void racingDecisionsLetExactlyOneWin() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);
        final String flowId = environment.newFlowId("customer-refund");
        controller.start(flowId, null, "normal");

        final ExecutorService executor = Executors.newFixedThreadPool(2);
        final CountDownLatch startGate = new CountDownLatch(1);
        final List<Integer> statuses = new ArrayList<>();
        String winner = "";
        try {
            final Future<ResponseEntity<Map<String, String>>> approve = executor.submit(() -> {
                startGate.await();
                return controller.decide(flowId, null, "approve", "Outage confirmed");
            });
            final Future<ResponseEntity<Map<String, String>>> reject = executor.submit(() -> {
                startGate.await();
                return controller.decide(flowId, null, "reject", "Credit offered instead");
            });
            startGate.countDown();
            for (Future<ResponseEntity<Map<String, String>>> decision : List.of(approve, reject)) {
                final ResponseEntity<Map<String, String>> response = decision.get(30, TimeUnit.SECONDS);
                statuses.add(response.getStatusCode().value());
                if (response.getStatusCode().value() == 200) {
                    winner = response.getBody().get("decision");
                }
            }
        } finally {
            executor.shutdownNow();
        }

        // The loser meets the lock or the closed decision (409), or the closed run (404).
        final List<Integer> sorted = statuses.stream().sorted().toList();
        assertTrue(sorted.equals(List.of(200, 409)) || sorted.equals(List.of(200, 404)), "statuses " + statuses);
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        final boolean approved = winner.equals("approve");
        assertEquals(approved ? "refunded" : "denied", result.getSingleOutput(String.class));
        assertEquals(approved ? 1 : 0, refundsOf(environment, flowId).size());
    }

    @Test
    void badRequestsReturnClientErrors() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);
        final String missingFlowId = environment.newFlowId("customer-refund-missing");

        assertDecision(400, "scenario must be one of normal, fail-then-succeed, deny, small",
                controller.start(null, null, "huge"));
        assertDecision(400, "flowId is required", controller.decide(null, "", "approve", "Outage confirmed"));
        final ResponseEntity<?> describedWithoutFlowId = controller.describe("", null);
        assertEquals(400, describedWithoutFlowId.getStatusCode().value());
        assertEquals(Map.of("error", "flowId is required"), describedWithoutFlowId.getBody());
        assertDecision(400, "a decision needs a reason", controller.decide(missingFlowId, null, "approve", " "));
        assertEquals(404, controller.describe(missingFlowId, null).getStatusCode().value());
        assertDecision(404, "no running refund has Flow ID " + missingFlowId,
                controller.decide(missingFlowId, null, "approve", "Outage confirmed"));
    }

    private static CustomerRefundController newController(final IntegEnvironment environment) {
        return new CustomerRefundController(environment.client(), environment.customerRefundFlow());
    }

    private static void awaitManager(final IntegEnvironment environment, final String flowId)
            throws InterruptedException {
        environment.awaitAttribute(
                flowId, CustomerRefundFlow.CASE_STATUS, CustomerRefundFlow.AWAITING_MANAGER, WAIT_TIMEOUT);
    }

    private static void assertDecision(
            final int status, final String error, final ResponseEntity<Map<String, String>> response) {
        assertEquals(status, response.getStatusCode().value(), String.valueOf(response.getBody()));
        assertEquals(error, response.getBody().get("error"));
    }

    /** Asserts the exact describe JSON; a null refundId must stay a JSON null. */
    private static void assertDescription(
            final ResponseEntity<?> response,
            final String flowId,
            final String status,
            final String caseStatus,
            final String refundId) {
        assertEquals(200, response.getStatusCode().value(), String.valueOf(response.getBody()));
        final ObjectNode expected = JSON.createObjectNode();
        expected.put("flowID", flowId);
        expected.put("status", status);
        expected.put("caseStatus", caseStatus);
        expected.put("refundId", refundId);
        final JsonNode actual = JSON.valueToTree(response.getBody());
        assertEquals(expected, actual);
    }

    /** Returns the one refund this environment's ledger holds for flowId; another Worker's refund fails. */
    private static RecordedRefund onlyRefundOf(final IntegEnvironment environment, final String flowId) {
        final List<RecordedRefund> refunds = refundsOf(environment, flowId);
        assertEquals(1, refunds.size(), "refunds " + refunds);
        return refunds.get(0);
    }

    private static List<RecordedRefund> refundsOf(final IntegEnvironment environment, final String flowId) {
        final FakeBillingService billing = new FakeBillingService(
                CustomerRefundConfig.billingLedger(environment.customerRefundLedgerDirectory()));
        return billing.listRefunds().stream()
                .filter(refund -> refund.idempotencyKey().equals("refund-" + flowId))
                .toList();
    }

    private static List<String> emailKeys(final IntegEnvironment environment) {
        return new FakeEmailService(
                CustomerRefundConfig.emailLedger(environment.customerRefundLedgerDirectory())).listSentKeys();
    }
}
