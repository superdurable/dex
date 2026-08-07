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

package io.superdurable.dex.controller;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.workflow.polling.PollingFlow;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/polling")
public class PollingController {
    private final Client client;
    private final PollingFlow flow;

    public PollingController(final Client client, final PollingFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<?> start(
            @RequestParam final String workflowId,
            @RequestParam final int pollingCompletionThreshold) {
        final String runId = client.startFlow(
                flow,
                workflowId,
                pollingCompletionThreshold,
                ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/complete")
    public ResponseEntity<?> complete(
            @RequestParam final String workflowId,
            @RequestParam final String channel) {
        final Channel<Void> target;
        if (PollingFlow.TASK_A_COMPLETED.equals(channel)) {
            target = flow.taskACompleted;
        } else if (PollingFlow.TASK_B_COMPLETED.equals(channel)) {
            target = flow.taskBCompleted;
        } else {
            final Map<String, String> error = new LinkedHashMap<String, String>();
            error.put("error", "channel must identify task A or task B");
            return ResponseEntity.status(HttpStatus.BAD_REQUEST).body(error);
        }
        client.publish(workflowId, target, (Void) null);
        return ResponseEntity.ok(Collections.<String, Object>emptyMap());
    }
}
