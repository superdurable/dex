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

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import io.superdurable.dex.AttributeValues;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowClosingException;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.RequestTimeoutException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.DecisionReason;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.NotAwaitingManagerException;
import io.superdurable.dex.products.customerrefund.CustomerRefundFlow.RefundRequest;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;

@RestController
@RequestMapping("/products/customer-refund/manager-approval")
public class CustomerRefundController {
    private static final Duration START_READY_TIMEOUT = Duration.ofSeconds(10);

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
            @RequestParam(required = false) final String scenario) {
        final String scenarioName = isEmpty(scenario) ? "normal" : scenario;
        final RefundRequest request = RefundSettings.SCENARIOS.get(scenarioName);
        if (request == null) {
            return error(HttpStatus.BAD_REQUEST, "scenario must be one of "
                    + String.join(", ", RefundSettings.SCENARIOS.keySet()));
        }
        final String chosenFlowId = optionalFlowId(flowId, workflowId);
        final String newFlowId = chosenFlowId.isEmpty() ? "customer-refund-" + System.nanoTime() : chosenFlowId;
        final String runId;
        try {
            runId = client.startFlow(flow, newFlowId, request, ExampleFlows.startOptions());
        } catch (final FlowAlreadyStartedException alreadyStarted) {
            return error(HttpStatus.CONFLICT, "refund " + newFlowId + " is already started");
        }
        try {
            // Like order-processing, return once the run reached its first decision point.
            client.waitForStepCompletion(
                    newFlowId,
                    StepExecutionId.of("CheckPolicy"),
                    WaitForStepCompletionOptions.newBuilder().requestTimeout(START_READY_TIMEOUT).build());
        } catch (final RequestTimeoutException notReady) {
            return error(HttpStatus.GATEWAY_TIMEOUT, "refund " + newFlowId + " did not check its policy in time");
        } catch (final FlowNotActiveOrNotFoundException alreadyClosed) {
            // A small or denied refund can close before the wait starts.
        }
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", newFlowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/decide")
    public ResponseEntity<Map<String, String>> decide(
            @RequestParam(required = false) final String flowId,
            @RequestParam(required = false) final String workflowId,
            @RequestParam(defaultValue = "") final String decision,
            @RequestParam(defaultValue = "") final String reason) {
        final String requiredFlowId = optionalFlowId(flowId, workflowId);
        if (requiredFlowId.isEmpty()) {
            return error(HttpStatus.BAD_REQUEST, "flowId is required");
        }
        if (!decision.equals("approve") && !decision.equals("reject")) {
            return error(HttpStatus.BAD_REQUEST, "decision must be approve or reject");
        }
        if (reason.isBlank()) {
            return error(HttpStatus.BAD_REQUEST, "a decision needs a reason");
        }
        final ResponseEntity<Map<String, String>> notFound =
                error(HttpStatus.NOT_FOUND, "no running refund has Flow ID " + requiredFlowId);
        if (!isRefundFlow(requiredFlowId)) {
            return notFound;
        }
        final CustomerRefundFlow stub = client.newRpcStub(CustomerRefundFlow.class, requiredFlowId);
        try {
            if (decision.equals("approve")) {
                client.invokeRPC(stub::approveRefund, new DecisionReason(reason));
            } else {
                client.invokeRPC(stub::rejectRefund, new DecisionReason(reason));
            }
        } catch (final WorkerInvocationException rejected) {
            if (!NotAwaitingManagerException.class.getName().equals(rejected.getWorkerErrorType())) {
                throw rejected;
            }
            return error(HttpStatus.CONFLICT, rejected.getWorkerErrorDetail());
        } catch (final RpcLockConflictException lockConflict) {
            return error(HttpStatus.CONFLICT, "another decision for this refund is in progress");
        } catch (final FlowClosingException closing) {
            return error(HttpStatus.CONFLICT, "this refund is closing");
        } catch (final FlowNotActiveOrNotFoundException notActive) {
            return notFound;
        }
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", requiredFlowId);
        response.put("decision", decision);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/describe")
    public ResponseEntity<?> describe(
            @RequestParam(required = false) final String flowId,
            @RequestParam(required = false) final String workflowId) {
        final String requiredFlowId = optionalFlowId(flowId, workflowId);
        if (requiredFlowId.isEmpty()) {
            return error(HttpStatus.BAD_REQUEST, "flowId is required");
        }
        final ResponseEntity<Map<String, String>> notFound =
                error(HttpStatus.NOT_FOUND, "no refund has Flow ID " + requiredFlowId);
        final FlowInfo run;
        final AttributeValues values;
        try {
            run = client.describeFlow(requiredFlowId);
            if (!flow.getFlowType().equals(run.getFlowType())) {
                return notFound;
            }
            values = client.getAttributes(
                    requiredFlowId, CustomerRefundFlow.CASE_STATUS, CustomerRefundFlow.REFUND_ID);
        } catch (final FlowNotFoundException missing) {
            return notFound;
        }
        return ResponseEntity.ok(new RefundDescription(
                requiredFlowId,
                run.getStatus().name().toLowerCase(Locale.ROOT),
                values.get(CustomerRefundFlow.CASE_STATUS).orElse(null),
                values.get(CustomerRefundFlow.REFUND_ID).orElse(null)));
    }

    // Keep missing values as JSON null, overriding the application's NON_NULL default.
    @JsonInclude(JsonInclude.Include.ALWAYS)
    record RefundDescription(
            @JsonProperty("flowID") String flowId,
            String status,
            String caseStatus,
            String refundId) {
    }

    /** Returns whether flowId names a run of this example's Flow type. */
    private boolean isRefundFlow(final String flowId) {
        try {
            return flow.getFlowType().equals(client.describeFlow(flowId).getFlowType());
        } catch (final FlowNotFoundException missing) {
            return false;
        }
    }

    /** Returns workflowId when set, else flowId, else an empty string. */
    private static String optionalFlowId(final String flowId, final String workflowId) {
        if (!isEmpty(workflowId)) {
            return workflowId;
        }
        return flowId == null ? "" : flowId;
    }

    private static boolean isEmpty(final String value) {
        return value == null || value.isEmpty();
    }

    private static ResponseEntity<Map<String, String>> error(final HttpStatus status, final String message) {
        return ResponseEntity.status(status).body(Map.of("error", message));
    }
}
