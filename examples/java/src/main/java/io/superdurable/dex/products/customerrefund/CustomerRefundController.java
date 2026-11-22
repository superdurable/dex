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

package io.superdurable.dex.products.customerrefund;

import io.superdurable.dex.AttributeValues;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.DecisionReason;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.RefundRequest;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;

import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/products/customer-refund/manager-approval")
public class CustomerRefundController {
    private final Client client;
    private final CustomerRefundFlow flow;

    public CustomerRefundController(
            final Client client,
            final CustomerRefundFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam(required = false) final String flowId,
            @RequestParam(required = false) final String workflowId,
            @RequestParam(defaultValue = "normal") final String scenario) {
        final RefundRequest request = RefundSettings.SCENARIOS.get(scenario);
        if (request == null) {
            return error(HttpStatus.BAD_REQUEST, "scenario must be one of "
                    + String.join(", ", RefundSettings.SCENARIOS.keySet()));
        }
        final String chosenFlowId = optionalFlowId(flowId, workflowId);
        final String newFlowId = chosenFlowId.isEmpty() ? "customer-refund-" + System.nanoTime() : chosenFlowId;
        final String runId = client.startFlow(flow, newFlowId, request, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", newFlowId);
        response.put("runID", runId);
        response.put("scenario", scenario);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/decide")
    public ResponseEntity<Map<String, String>> decide(
            @RequestParam(required = false) final String flowId,
            @RequestParam(required = false) final String workflowId,
            @RequestParam(defaultValue = "") final String decision,
            @RequestParam(defaultValue = "") final String reason) {
        final String requiredFlowId = requiredFlowId(flowId, workflowId);
        if (!decision.equals("approve") && !decision.equals("reject")) {
            return error(HttpStatus.BAD_REQUEST, "decision must be approve or reject");
        }
        if (reason.isBlank()) {
            return error(HttpStatus.BAD_REQUEST, "a decision needs a reason");
        }
        final CustomerRefundFlow stub = client.newRpcStub(CustomerRefundFlow.class, requiredFlowId);
        try {
            if (decision.equals("approve")) {
                client.invokeRPC(stub::approveRefund, new DecisionReason(reason));
            } else {
                client.invokeRPC(stub::rejectRefund, new DecisionReason(reason));
            }
        } catch (final WorkerInvocationException rejected) {
            return rpcRejection(rejected);
        } catch (final RpcLockConflictException lockConflict) {
            return error(HttpStatus.CONFLICT, "another decision for this refund is in progress");
        } catch (final FlowNotActiveOrNotFoundException notActive) {
            return error(HttpStatus.NOT_FOUND, "no running refund has Flow ID " + requiredFlowId);
        }
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", requiredFlowId);
        response.put("decision", decision);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/describe")
    public ResponseEntity<Map<String, String>> describe(
            @RequestParam(required = false) final String flowId,
            @RequestParam(required = false) final String workflowId) {
        final String requiredFlowId = requiredFlowId(flowId, workflowId);
        final FlowInfo run;
        final AttributeValues values;
        try {
            run = client.describeFlow(requiredFlowId);
            values = client.getAttributes(
                    requiredFlowId, CustomerRefundFlow.CASE_STATUS, CustomerRefundFlow.REFUND_ID);
        } catch (final FlowNotFoundException notFound) {
            return error(HttpStatus.NOT_FOUND, "no refund has Flow ID " + requiredFlowId);
        }
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", requiredFlowId);
        response.put("status", run.getStatus().name());
        response.put("caseStatus", values.get(CustomerRefundFlow.CASE_STATUS).orElse(""));
        response.put("refundId", values.get(CustomerRefundFlow.REFUND_ID).orElse(""));
        return ResponseEntity.ok(response);
    }

    /** Returns the Flow ID from flowId, or from its alias workflowId. */
    private static String requiredFlowId(final String flowId, final String workflowId) {
        final String chosenFlowId = optionalFlowId(flowId, workflowId);
        if (chosenFlowId.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "flowId is required");
        }
        return chosenFlowId;
    }

    /** Returns workflowId when set, else flowId, else an empty string. */
    private static String optionalFlowId(final String flowId, final String workflowId) {
        if (workflowId != null && !workflowId.isEmpty()) {
            return workflowId;
        }
        return flowId == null ? "" : flowId;
    }

    /** Maps the RPC's own checks: a bad reason is 400, a refund not awaiting the manager is 409. */
    private static ResponseEntity<Map<String, String>> rpcRejection(final WorkerInvocationException rejected) {
        final String errorType = String.valueOf(rejected.getWorkerErrorType());
        if (errorType.endsWith("IllegalArgumentException")) {
            return error(HttpStatus.BAD_REQUEST, rejected.getWorkerErrorDetail());
        }
        if (errorType.endsWith("IllegalStateException")) {
            return error(HttpStatus.CONFLICT, rejected.getWorkerErrorDetail());
        }
        throw rejected;
    }

    private static ResponseEntity<Map<String, String>> error(final HttpStatus status, final String message) {
        return ResponseEntity.status(status).body(Map.of("error", message));
    }
}
