/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.flow;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.FlowTimeoutPolicy;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.WorkerTarget;
import java.time.Duration;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/primitives/flow")
public final class FlowController {
    private final Client client;
    private final ExampleFlow exampleFlow;

    public FlowController(final Client client, final ExampleFlow exampleFlow) {
        this.client = client;
        this.exampleFlow = exampleFlow;
    }

    private static StartFlowOptions exampleStartFlowOptions() {
        return StartFlowOptions.newBuilder()
                .timeout(Duration.ofMinutes(30))
                .timeoutPolicy(FlowTimeoutPolicy.HANDLER)
                .startDelay(Duration.ofMinutes(5))
                .idReusePolicy(IdReusePolicy.DISALLOW)
                .retryPolicy(
                        RetryPolicy.newBuilder()
                                .initialInterval(Duration.ofMinutes(1))
                                .backoffCoefficient(2)
                                .maximumInterval(Duration.ofMinutes(10))
                                .maximumAttempts(3)
                                .build())
                .addAttribute(ExampleFlow.status, "queued")
                .configOverride(
                        FlowConfig.newBuilder()
                                .stepDurability(StepDurability.SYNC)
                                .build())
                .ignoreAlreadyStarted(true)
                .requestId("start-order-123")
                .build();
    }

    private static void rerouteActiveFlow(final Client client, final String flowId) {
        client.updateFlowConfig(
                flowId,
                FlowConfig.newBuilder()
                        .workerTarget(new WorkerTarget("worker-canary:8803", false))
                        .build());
    }

    @GetMapping("/start")
    public String start(
            @RequestParam("workflowId") final String flowId,
            @RequestParam("inputNum") final int inputNum)
            throws Exception {
        return client.startFlow(
                exampleFlow,
                flowId,
                inputNum,
                StartFlowOptions.newBuilder()
                        .timeout(Duration.ofHours(1))
                        .configOverride(
                                FlowConfig.newBuilder()
                                        .stepDurability(StepDurability.SYNC)
                                        .build())
                        .build());
    }
}
