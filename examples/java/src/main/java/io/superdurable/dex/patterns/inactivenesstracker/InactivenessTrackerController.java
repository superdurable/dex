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

package io.superdurable.dex.patterns.inactivenesstracker;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/inactiveness-tracker-timer")
public class InactivenessTrackerController {
    private final Client client;
    private final InactivenessTrackerFlow inactivenessTrackerFlow;

    public InactivenessTrackerController(
            final Client client,
            final InactivenessTrackerFlow inactivenessTrackerFlow) {
        this.client = client;
        this.inactivenessTrackerFlow = inactivenessTrackerFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startInactivenessTracker(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                inactivenessTrackerFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/activity")
    ResponseEntity<String> recordActivity(@RequestParam final String workflowId) {
        final InactivenessTrackerFlow stub =
                client.newRpcStub(InactivenessTrackerFlow.class, workflowId);
        client.invokeRPC(stub::recordActivity);
        return ResponseEntity.ok("activity recorded");
    }
}
