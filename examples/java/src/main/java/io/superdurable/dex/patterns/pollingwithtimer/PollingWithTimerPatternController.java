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

package io.superdurable.dex.patterns.pollingwithtimer;

import io.superdurable.dex.Client;
import io.superdurable.dex.patterns.polling.FakeJobService;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.NoSuchElementException;

@RestController
@RequestMapping("/patterns/polling-with-timer")
public class PollingWithTimerPatternController {
    private final Client client;
    private final PollingWithTimerFlow pollingWithTimerFlow;
    private final FakeJobService jobs;

    public PollingWithTimerPatternController(
            final Client client,
            final PollingWithTimerFlow pollingWithTimerFlow,
            final FakeJobService jobs) {
        this.client = client;
        this.pollingWithTimerFlow = pollingWithTimerFlow;
        this.jobs = jobs;
    }

    @GetMapping("/start")
    ResponseEntity<String> startPollingWithTimer(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                pollingWithTimerFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/complete-job")
    ResponseEntity<String> completeJob(@RequestParam final String workflowId) {
        try {
            jobs.completeJob(workflowId);
        } catch (final NoSuchElementException jobNotFound) {
            return ResponseEntity.status(HttpStatus.NOT_FOUND).body(jobNotFound.getMessage());
        }
        return ResponseEntity.ok("done");
    }
}
