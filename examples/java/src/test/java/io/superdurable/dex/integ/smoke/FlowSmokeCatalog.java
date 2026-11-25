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

package io.superdurable.dex.integ.smoke;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.FlowStatus;

import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

final class FlowSmokeCatalog {
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private FlowSmokeCatalog() {
    }

    static List<FlowSmokeEntry> entries(final FlowSmokeEnvironment environment) {
        return List.of(
                FlowSmokeEntry.get("products/engagement", "/products/engagement/start", Map.of()),
                FlowSmokeEntry.get(
                        "products/microservices",
                        "/products/microservices/start",
                        Map.of("workflowId", environment.newFlowId("microservices"))),
                FlowSmokeEntry.get(
                        "products/money-transfer",
                        "/products/money-transfer/start",
                        Map.of(
                                "amount", "100",
                                "fromAccount", "from-smoke",
                                "toAccount", "to-smoke",
                                "notes", "smoke")),
                FlowSmokeEntry.get(
                        "products/order-processing",
                        "/products/order-processing/start",
                        Map.of()),
                FlowSmokeEntry.get(
                        "products/customer-refund/manager-approval",
                        "/products/customer-refund/manager-approval/start",
                        Map.of("flowId", environment.newFlowId("customer-refund"))),
                FlowSmokeEntry.get("products/subscription", "/products/subscription/start", Map.of()),
                FlowSmokeEntry.get(
                        "products/signup",
                        "/products/signup/submit",
                        signupQuery(environment)),
                FlowSmokeEntry.get(
                        "products/job-post",
                        "/products/job-post/create",
                        Map.of(
                                "title", "Smoke Test Job",
                                "description", "Smoke test description"),
                        FlowSmokeFlags.noStartStep()),
                FlowSmokeEntry.get(
                        "patterns/polling",
                        "/patterns/polling/start",
                        Map.of("workflowId", environment.newFlowId("pattern-polling"))),
                FlowSmokeEntry.get(
                        "patterns/fail-fast",
                        "/patterns/fail-fast/start",
                        Map.of(
                                "workflowId", environment.newFlowId("pattern-fail-fast"),
                                "amountCents", "12000",
                                "bankAccount", "12345678")),
                FlowSmokeEntry.get(
                        "patterns/control-long-running-work",
                        "/patterns/control-long-running-work/start",
                        Map.of("workflowId", environment.newFlowId("control-long-running-work"))),
                FlowSmokeEntry.get("patterns/reminders", "/patterns/reminders/start", Map.of()),
                entityFlow(environment),
                entityStore(environment),
                FlowSmokeEntry.post(
                        "patterns/sequentially-chunked-attribute-map",
                        "/patterns/sequentially-chunked-attribute-map/start",
                        null,
                        FlowSmokeFlags.noStartStep()),
                FlowSmokeEntry.post(
                        "patterns/hash-partitioned-attribute-map",
                        "/patterns/hash-partitioned-attribute-map/start",
                        null,
                        FlowSmokeFlags.noStartStep()),
                FlowSmokeEntry.get(
                        "patterns/manual-recovery",
                        "/patterns/manual-recovery/start",
                        Map.of("workflowId", environment.newFlowId("manual-recovery"))),
                FlowSmokeEntry.get(
                        "patterns/inactiveness-tracker-timer",
                        "/patterns/inactiveness-tracker-timer/start",
                        Map.of("workflowId", environment.newFlowId("inactiveness-tracker-timer"))),
                FlowSmokeEntry.get(
                        "patterns/inbound-event-loop",
                        "/patterns/inbound-event-loop/start",
                        Map.of("workflowId", environment.newFlowId("inbound-event-loop"))),
                FlowSmokeEntry.get(
                        "patterns/parallel/static",
                        "/patterns/parallel/start/static",
                        Map.of("workflowId", environment.newFlowId("parallel-static"))),
                FlowSmokeEntry.get(
                        "patterns/parallel/dynamic",
                        "/patterns/parallel/start/dynamic",
                        Map.of("workflowId", environment.newFlowId("parallel-dynamic"))),
                FlowSmokeEntry.get(
                        "patterns/parallel/await",
                        "/patterns/parallel/start/await",
                        Map.of("workflowId", environment.newFlowId("parallel-await"))),
                FlowSmokeEntry.get(
                        "patterns/parallel/first-win",
                        "/patterns/parallel/start/first-win",
                        Map.of("workflowId", environment.newFlowId("parallel-first-win"))),
                FlowSmokeEntry.get(
                        "patterns/recovery",
                        "/patterns/recovery/start",
                        Map.of(
                                "workflowId", environment.newFlowId("recovery"),
                                "itemName", "smoke-item",
                                "quantity", "2"),
                        FlowSmokeFlags.stepStartMayFail()),
                parallelSubFlows(environment, "basic"),
                parallelSubFlows(environment, "wait-for-half"),
                parallelSubFlows(environment, "long-lived-parent"),
                parallelSubFlows(environment, "short-lived-parent"),
                childFlowsBusinessIdentity(),
                FlowSmokeEntry.get(
                        "patterns/drain-channels/internal",
                        "/patterns/drain-channels/internal/start",
                        Map.of("workflowId", environment.newFlowId("drain-internal"))),
                FlowSmokeEntry.get(
                        "patterns/drain-channels/external-publishing",
                        "/patterns/drain-channels/external-publishing/start-or-publish",
                        Map.of("workflowId", environment.newFlowId("drain-external"))),
                FlowSmokeEntry.get(
                        "patterns/wait-for-step-completion",
                        "/patterns/wait-for-step-completion/start",
                        Map.of("workflowId", environment.newFlowId("wait-for-state"))),
                FlowSmokeEntry.get(
                        "patterns/timeout",
                        "/patterns/timeout/start",
                        Map.of(
                                "workflowId", environment.newFlowId("timeout"),
                                "successfulWorkflow", "true")),
                FlowSmokeEntry.get(
                        "primitives/step",
                        "/primitives/step/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step"),
                                "inputNum", "1")),
                FlowSmokeEntry.get(
                        "primitives/step/retry",
                        "/primitives/step/retry/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-retry"),
                                "readyAfterAttempt", "2")),
                FlowSmokeEntry.get(
                        "primitives/step/custom-retry",
                        "/primitives/step/custom-retry/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-custom-retry"),
                                "readyAfterAttempt", "1")),
                FlowSmokeEntry.get(
                        "primitives/step/durability",
                        "/primitives/step/durability/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-durability"),
                                "mode", "sync")),
                FlowSmokeEntry.get(
                        "primitives/step/heartbeat",
                        "/primitives/step/heartbeat/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-heartbeat"),
                                "batches", "0")),
                FlowSmokeEntry.get(
                        "primitives/step/options-override",
                        "/primitives/step/options-override/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-options-override"),
                                "input", "smoke")),
                FlowSmokeEntry.get(
                        "primitives/step/step-decision",
                        "/primitives/step/step-decision/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-decision"),
                                "mode", "graceful")),
                FlowSmokeEntry.get(
                        "primitives/step/wait-types",
                        "/primitives/step/wait-types/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-step-wait-types"),
                                "mode", "any",
                                "timeoutSeconds", "1")),
                FlowSmokeEntry.get(
                        "primitives/attribute",
                        "/primitives/attribute/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-attribute"),
                                "message", "smoke")),
                FlowSmokeEntry.get(
                        "primitives/channel",
                        "/primitives/channel/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-channel"),
                                "inputNum", "1")),
                FlowSmokeEntry.get(
                        "primitives/stream",
                        "/primitives/stream/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-stream"),
                                "input", "smoke")),
                FlowSmokeEntry.get(
                        "primitives/timer",
                        "/primitives/timer/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-timer"),
                                "seconds", "1")),
                FlowSmokeEntry.get(
                        "primitives/rpc",
                        "/primitives/rpc/start",
                        Map.of("workflowId", environment.newFlowId("primitive-rpc"))),
                FlowSmokeEntry.get(
                        "primitives/rpc-canceling-steps",
                        "/primitives/rpc-canceling-steps/start",
                        Map.of(
                                "workflowId",
                                environment.newFlowId("primitive-rpc-canceling-steps"),
                                "reportName",
                                "smoke")),
                FlowSmokeEntry.get(
                        "primitives/subflow",
                        "/primitives/subflow/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-subflow"),
                                "inputNum", "1")),
                FlowSmokeEntry.get(
                        "primitives/client-apis",
                        "/primitives/client-apis/start",
                        Map.of(
                                "workflowId", environment.newFlowId("primitive-client-apis"),
                                "keyword", "smoke")));
    }

    private static FlowSmokeEntry parallelSubFlows(
            final FlowSmokeEnvironment environment,
            final String kind) {
        return FlowSmokeEntry.get(
                "patterns/parallel-subflows/" + kind,
                "/patterns/parallel-subflows/start/" + kind,
                Map.of("workflowId", environment.newFlowId("parallel-subflows-" + kind)));
    }

    // Starts an order, then finds its day-long shipment by shipment number.
    private static FlowSmokeEntry childFlowsBusinessIdentity() {
        return FlowSmokeEntry.custom(
                "patterns/child-flows-business-identity",
                FlowSmokeFlags.none(),
                env -> {
                    final String orderId = env.newFlowId("child-flows-order");
                    final FlowSmokeTriggerResult result = env.triggerHttp(
                            "GET",
                            "/patterns/child-flows-business-identity/start",
                            Map.of("workflowId", orderId),
                            null);
                    verifyShipmentLookupByNumber(env, orderId);
                    return result;
                });
    }

    private static void verifyShipmentLookupByNumber(
            final FlowSmokeEnvironment environment, final String orderId) throws Exception {
        final String shipmentNumber = orderId + "-2";
        final long deadline = System.nanoTime() + Duration.ofSeconds(30).toNanos();
        String lastBody = "";
        boolean running = false;
        while (!running && System.nanoTime() < deadline) {
            final HttpResponse<String> response =
                    getChildFlowsRoute(environment, "/shipment", shipmentNumber);
            lastBody = response.body();
            running = response.statusCode() == 200
                    && "running".equals(OBJECT_MAPPER.readTree(lastBody).path("status").asText());
            if (!running) {
                Thread.sleep(200L);
            }
        }
        if (!running) {
            throw new AssertionError("shipment lookup for " + shipmentNumber + " last=" + lastBody);
        }

        // The order is not a shipment: both shipment routes answer 404 and leave it running.
        for (final String route : List.of("/shipment", "/cancel-shipment")) {
            final HttpResponse<String> response = getChildFlowsRoute(environment, route, orderId);
            if (response.statusCode() != 404) {
                throw new AssertionError(route + " for order " + orderId + " returned "
                        + response.statusCode() + ": " + response.body());
            }
        }
        final FlowStatus orderStatus = environment.client().describeFlow(orderId).getStatus();
        if (orderStatus != FlowStatus.RUNNING) {
            throw new AssertionError("order " + orderId + " is " + orderStatus);
        }

        final HttpResponse<String> canceled =
                getChildFlowsRoute(environment, "/cancel-shipment", shipmentNumber);
        if (canceled.statusCode() != 200) {
            throw new AssertionError("cancel-shipment returned " + canceled.statusCode() + ": "
                    + canceled.body());
        }
        FlowStatus shipmentStatus = null;
        final long cancelDeadline = System.nanoTime() + Duration.ofSeconds(30).toNanos();
        while (System.nanoTime() < cancelDeadline) {
            shipmentStatus = environment.client().describeFlow(shipmentNumber).getStatus();
            if (shipmentStatus == FlowStatus.CANCELED) {
                return;
            }
            Thread.sleep(200L);
        }
        throw new AssertionError("shipment " + shipmentNumber + " is " + shipmentStatus);
    }

    private static HttpResponse<String> getChildFlowsRoute(
            final FlowSmokeEnvironment environment, final String route, final String shipmentNumber)
            throws Exception {
        return environment.get(
                "/patterns/child-flows-business-identity" + route,
                Map.of("shipmentNumber", shipmentNumber));
    }

    private static Map<String, String> signupQuery(final FlowSmokeEnvironment environment) {
        final String username = environment.newFlowId("signup");
        return Map.of(
                "username", username,
                "email", username + "@example.com");
    }

    private static FlowSmokeEntry entityFlow(final FlowSmokeEnvironment environment) {
        return FlowSmokeEntry.custom(
                "patterns/entity-flow",
                FlowSmokeFlags.noStartStep(),
                env -> {
                    final String accountId = env.newFlowId("entity-flow");
                    final Map<String, Object> body = new LinkedHashMap<>();
                    body.put("accountId", accountId);
                    body.put("displayName", "Smoke Tester");
                    body.put("email", accountId + "@example.com");
                    return env.triggerHttp("POST", "/patterns/entity-flow/account", Map.of(), body);
                });
    }

    private static FlowSmokeEntry entityStore(final FlowSmokeEnvironment environment) {
        return FlowSmokeEntry.custom(
                "patterns/entity-store",
                FlowSmokeFlags.noStartStep(),
                env -> {
                    final String userId = env.newFlowId("entity-store");
                    final Map<String, Object> body = new LinkedHashMap<>();
                    body.put("userId", userId);
                    body.put("displayName", "Smoke Tester");
                    body.put("email", userId + "@example.com");
                    body.put("marketingOptIn", true);
                    body.put("credits", 120);
                    body.put("weight", 59.5);
                    body.put("lastLoggedInTime", "2026-08-11T15:30:00Z");
                    body.put("metadata", Map.of("source", "smoke", "tags", List.of("example")));
                    final FlowSmokeTriggerResult result =
                            env.triggerHttp(
                                    "POST", "/patterns/entity-store/profile", Map.of(), body);
                    final String flowId = result.flowId.isEmpty() ? userId : result.flowId;
                    return new FlowSmokeTriggerResult(flowId, result.runId);
                });
    }
}
