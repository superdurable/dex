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

package io.superdurable.dex.primitives.waittypes;

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
@RequestMapping("/primitives/step/wait-types")
public final class WaitTypesController {
    private final Client client;
    private final WaitTypesFlow flow;

    public WaitTypesController(final Client client, final WaitTypesFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId,
            @RequestParam final String mode,
            @RequestParam(defaultValue = "60") final int timeoutSeconds) {
        final String runId = client.startFlow(
                flow,
                workflowId,
                new WaitTypesInput(mode, timeoutSeconds),
                ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/signal-a")
    public ResponseEntity<String> signalA(@RequestParam final String workflowId) {
        final WaitTypesFlow stub = client.newRpcStub(WaitTypesFlow.class, workflowId);
        client.invokeRPC(stub::signalA);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/signal-b")
    public ResponseEntity<String> signalB(@RequestParam final String workflowId) {
        final WaitTypesFlow stub = client.newRpcStub(WaitTypesFlow.class, workflowId);
        client.invokeRPC(stub::signalB);
        return ResponseEntity.ok("done");
    }
}
