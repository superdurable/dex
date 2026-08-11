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

package io.superdurable.dex.controller;

import io.superdurable.dex.Client;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.workflow.retryingfailure.RetryingFailureFlow;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/retrying-failure")
public final class RetryingFailureController {
    private static final Duration FLOW_TIMEOUT = Duration.ofHours(24);

    private final Client client;
    private final RetryingFailureFlow flow;

    public RetryingFailureController(final Client client, final RetryingFailureFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                flow,
                workflowId,
                null,
                StartFlowOptions.newBuilder().timeout(FLOW_TIMEOUT).build());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/stop")
    public ResponseEntity<Map<String, String>> stop(@RequestParam final String workflowId) {
        client.stopFlow(
                workflowId,
                new StopFlowOptions(StopType.TERMINATE, "retry demonstration finished"));
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("status", "terminated");
        return ResponseEntity.ok(response);
    }
}
