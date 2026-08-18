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

import io.superdurable.dex.products.shortlistcandidates.WorkflowIds;

import java.util.List;
import java.util.Map;

final class FlowSmokeCatalog {
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
                        "products/polling",
                        "/products/polling/start",
                        Map.of(
                                "workflowId", environment.newFlowId("product-polling"),
                                "pollingCompletionThreshold", "3")),
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
                shortlistOptIn(environment),
                shortlist(environment),
                FlowSmokeEntry.get(
                        "patterns/polling/simple",
                        "/patterns/polling/start/simple",
                        Map.of("workflowId", environment.newFlowId("pattern-polling-simple"))),
                FlowSmokeEntry.get(
                        "patterns/polling/backoff",
                        "/patterns/polling/start/backoff",
                        Map.of("workflowId", environment.newFlowId("pattern-polling-backoff"))),
                FlowSmokeEntry.get(
                        "patterns/interruptible",
                        "/patterns/interruptible/start",
                        Map.of("workflowId", environment.newFlowId("interruptible"))),
                FlowSmokeEntry.get("patterns/reminders", "/patterns/reminders/start", Map.of()),
                FlowSmokeEntry.get(
                        "patterns/intervention",
                        "/patterns/intervention/start",
                        Map.of("workflowId", environment.newFlowId("intervention"))),
                FlowSmokeEntry.get(
                        "patterns/resettable-timer",
                        "/patterns/resettable-timer/start",
                        Map.of("workflowId", environment.newFlowId("resettable-timer"))),
                FlowSmokeEntry.get(
                        "patterns/parallel/simple",
                        "/patterns/parallel/start/simple",
                        Map.of("workflowId", environment.newFlowId("parallel-simple"))),
                FlowSmokeEntry.get(
                        "patterns/parallel/with-await",
                        "/patterns/parallel/start/withAwait",
                        Map.of("workflowId", environment.newFlowId("parallel-await"))),
                FlowSmokeEntry.get(
                        "patterns/recovery",
                        "/patterns/recovery/start",
                        Map.of(
                                "workflowId", environment.newFlowId("recovery"),
                                "itemName", "smoke-item",
                                "quantity", "2"),
                        FlowSmokeFlags.stepStartMayFail()),
                FlowSmokeEntry.get(
                        "patterns/scalable-parallel",
                        "/patterns/scalable-parallel/start",
                        Map.of(
                                "workflowId", environment.newFlowId("scalable-parallel"),
                                "numOfChildWfs", "1")),
                FlowSmokeEntry.get(
                        "patterns/parent-child",
                        "/patterns/parent-child/start",
                        Map.of(
                                "workflowId", environment.newFlowId("parent-child"),
                                "numOfChildWfs", "1")),
                FlowSmokeEntry.get(
                        "patterns/drain-channels/internal",
                        "/patterns/drain-channels/internal/start",
                        Map.of("workflowId", environment.newFlowId("drain-internal"))),
                FlowSmokeEntry.get(
                        "patterns/drain-channels/signal",
                        "/patterns/drain-channels/signal/startorsignal",
                        Map.of("workflowId", environment.newFlowId("drain-signal"))),
                FlowSmokeEntry.get(
                        "patterns/wait-for-state-completion",
                        "/patterns/wait-for-state-completion/start",
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
                                "readyAfterAttempt", "2"),
                        FlowSmokeFlags.stepStartMayFail()));
    }

    private static Map<String, String> signupQuery(final FlowSmokeEnvironment environment) {
        final String username = environment.newFlowId("signup");
        return Map.of(
                "username", username,
                "email", username + "@example.com");
    }

    private static FlowSmokeEntry shortlistOptIn(final FlowSmokeEnvironment environment) {
        return FlowSmokeEntry.custom(
                "products/shortlist-candidates/employer-opt-in",
                FlowSmokeFlags.none(),
                env -> {
                    final String employerId = env.newFlowId("employer");
                    final Map<String, String> body = Map.of("employerId", employerId);
                    env.triggerHttp("POST", "/products/shortlist-candidates/opt_in", Map.of(), body);
                    return new FlowSmokeTriggerResult(
                            WorkflowIds.employerOptIn(employerId), "");
                });
    }

    private static FlowSmokeEntry shortlist(final FlowSmokeEnvironment environment) {
        return FlowSmokeEntry.custom(
                "products/shortlist-candidates/shortlist",
                FlowSmokeFlags.none(),
                env -> {
                    final String employerId = env.newFlowId("shortlist-employer");
                    final String candidateId = env.newFlowId("candidate");
                    env.triggerHttp(
                            "POST",
                            "/products/shortlist-candidates/opt_in",
                            Map.of(),
                            Map.of("employerId", employerId));
                    final FlowSmokeTriggerResult result =
                            env.triggerHttp(
                                    "POST",
                                    "/products/shortlist-candidates/shortlist",
                                    Map.of(),
                                    Map.of(
                                            "employerId", employerId,
                                            "candidateId", candidateId));
                    return new FlowSmokeTriggerResult(
                            WorkflowIds.shortlist(employerId, candidateId), result.runId);
                });
    }
}
