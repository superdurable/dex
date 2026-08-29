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

package io.superdurable.dex.patterns.timeout;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowTimeoutPolicy;
import io.superdurable.dex.StartFlowOptions;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;

@RestController
@RequestMapping("/patterns/timeout")
public class TimeoutController {
    private final Client client;
    private final FlowGracefulTimeout flowGracefulTimeout;

    public TimeoutController(
            final Client client,
            final FlowGracefulTimeout flowGracefulTimeout) {
        this.client = client;
        this.flowGracefulTimeout = flowGracefulTimeout;
    }

    @GetMapping("/start")
    ResponseEntity<String> startTimeoutWorkflow(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "true") final Boolean successfulWorkflow) {
        client.startFlow(
                flowGracefulTimeout,
                workflowId,
                successfulWorkflow,
                StartFlowOptions.newBuilder()
                        .timeout(Duration.ofMinutes(1))
                        .timeoutPolicy(FlowTimeoutPolicy.HANDLER)
                        .build());
        return ResponseEntity.ok(String.format("success for workflow %s", workflowId));
    }
}
