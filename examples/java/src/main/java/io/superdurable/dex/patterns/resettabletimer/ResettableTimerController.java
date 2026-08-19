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

package io.superdurable.dex.patterns.resettabletimer;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/resettable-timer")
public class ResettableTimerController {
    private final Client client;
    private final ResettableTimerFlow resettableTimerFlow;

    public ResettableTimerController(
            final Client client,
            final ResettableTimerFlow resettableTimerFlow) {
        this.client = client;
        this.resettableTimerFlow = resettableTimerFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startResettableTimer(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                resettableTimerFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/reset")
    ResponseEntity<String> resetResettableTimer(@RequestParam final String workflowId) {
        final ResettableTimerFlow stub =
                client.newRpcStub(ResettableTimerFlow.class, workflowId);
        client.invokeRPC(stub::sendResetMessage);
        return ResponseEntity.ok("reset");
    }
}
