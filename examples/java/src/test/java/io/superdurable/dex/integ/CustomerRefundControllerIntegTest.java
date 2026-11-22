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
import io.superdurable.dex.products.customerrefund.CustomerRefundConfig;
import io.superdurable.dex.products.customerrefund.CustomerRefundController;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow;
import io.superdurable.dex.products.customerrefund.FakeBillingService;
import io.superdurable.dex.products.customerrefund.FakeBillingService.RecordedRefund;
import io.superdurable.dex.products.customerrefund.FakeEmailService;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.function.Executable;
import org.springframework.http.ResponseEntity;
import org.springframework.web.server.ResponseStatusException;

import java.time.Duration;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Drives the customer refund controller against the shared integ Worker. */
@ExtendWith(SharedIntegExtension.class)
public class CustomerRefundControllerIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    @Test
    void managerApprovesThroughTheController() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);

        final ResponseEntity<Map<String, String>> started = controller.start(null, null, "normal");
        assertEquals(200, started.getStatusCode().value());
        final String flowId = started.getBody().get("flowID");
        assertTrue(flowId.startsWith("customer-refund-"), flowId);
        assertTrue(!started.getBody().get("runID").isEmpty());
        awaitManager(environment, flowId);

        assertDecision(400, "a decision needs a reason", controller.decide(flowId, null, "approve", "  "));
        assertDecision(400, "decision must be approve or reject", controller.decide(flowId, null, "maybe", "Why"));
        assertEquals(200, controller.decide(flowId, null, "approve", "Outage confirmed").getStatusCode().value());
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals("refunded", result.getSingleOutput(String.class));

        final Map<String, String> described = controller.describe(null, flowId).getBody();
        assertEquals(flowId, described.get("flowID"));
        assertEquals("COMPLETED", described.get("status"));
        assertEquals("closed-refunded", described.get("caseStatus"));
        final List<RecordedRefund> refunds = refundsOf(environment, flowId);
        assertEquals(1, refunds.size());
        assertEquals(refunds.get(0).refundId(), described.get("refundId"));
        assertTrue(emailKeys(environment).contains("email-" + flowId));
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
        final Map<String, String> described = controller.describe(flowId, null).getBody();
        assertEquals("closed-denied", described.get("caseStatus"));
        assertEquals("", described.get("refundId"));
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
        assertEquals(1, refundsOf(environment, flowId).size());
    }

    @Test
    void badRequestsReturnClientErrors() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final CustomerRefundController controller = newController(environment);
        final String missingFlowId = environment.newFlowId("customer-refund-missing");

        assertEquals(400, controller.start(null, null, "huge").getStatusCode().value());
        assertFlowIdRequired(() -> controller.decide(null, "", "approve", "Outage confirmed"));
        assertFlowIdRequired(() -> controller.describe("", null));
        assertEquals(404, controller.describe(missingFlowId, null).getStatusCode().value());
        assertEquals(404,
                controller.decide(missingFlowId, null, "approve", "Outage confirmed").getStatusCode().value());
    }

    private static void assertFlowIdRequired(final Executable call) {
        final ResponseStatusException missing = assertThrows(ResponseStatusException.class, call);
        assertEquals(400, missing.getStatusCode().value());
        assertEquals("flowId is required", missing.getReason());
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
