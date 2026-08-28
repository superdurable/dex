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

package io.superdurable.dex.patterns.parallel;

import io.superdurable.dex.Client;
import io.superdurable.dex.Flow;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/parallel")
public class ParallelController {
    private final Client client;
    private final StaticParallelStepsFlow staticFlow;
    private final DynamicParallelStepsFlow dynamicFlow;
    private final AwaitParallelStepsFlow awaitFlow;
    private final FirstWinParallelStepsFlow firstWinFlow;

    public ParallelController(
            final Client client,
            final StaticParallelStepsFlow staticFlow,
            final DynamicParallelStepsFlow dynamicFlow,
            final AwaitParallelStepsFlow awaitFlow,
            final FirstWinParallelStepsFlow firstWinFlow) {
        this.client = client;
        this.staticFlow = staticFlow;
        this.dynamicFlow = dynamicFlow;
        this.awaitFlow = awaitFlow;
        this.firstWinFlow = firstWinFlow;
    }

    @GetMapping("/start/static")
    ResponseEntity<String> startStatic(@RequestParam final String workflowId) {
        return start(staticFlow, workflowId, "work");
    }

    @GetMapping("/start/dynamic")
    ResponseEntity<String> startDynamic(@RequestParam final String workflowId) {
        return start(dynamicFlow, workflowId, 3);
    }

    @GetMapping("/start/await")
    ResponseEntity<String> startAwait(@RequestParam final String workflowId) {
        return start(awaitFlow, workflowId, 3);
    }

    @GetMapping("/start/first-win")
    ResponseEntity<String> startFirstWin(@RequestParam final String workflowId) {
        return start(firstWinFlow, workflowId, 3);
    }

    private <T> ResponseEntity<String> start(
            final Flow<T> flow, final String workflowId, final T input) {
        final String runId = client.startFlow(flow, workflowId, input, ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }
}
