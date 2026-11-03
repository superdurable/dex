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

package io.superdurable.dex.primitives.rpccancelingsteps;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/primitives/rpc-canceling-steps")
public final class ReportGenerationController {
    private final Client client;
    private final ReportGenerationFlow flow;

    public ReportGenerationController(final Client client, final ReportGenerationFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId,
            @RequestParam final String reportName) {
        final String runId =
                client.startFlow(flow, workflowId, reportName, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/stop")
    public ResponseEntity<Map<String, Boolean>> stop(@RequestParam final String workflowId) {
        final ReportGenerationFlow stub = client.newRpcStub(ReportGenerationFlow.class, workflowId);
        final Map<String, Boolean> response = new LinkedHashMap<String, Boolean>();
        response.put("isStopping", client.invokeRPC(stub::stopReport));
        return ResponseEntity.ok(response);
    }
}
