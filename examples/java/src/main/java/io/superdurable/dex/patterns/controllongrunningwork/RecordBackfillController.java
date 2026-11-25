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

package io.superdurable.dex.patterns.controllongrunningwork;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/control-long-running-work")
public class RecordBackfillController {
    private final Client client;
    private final RecordBackfillFlow recordBackfillFlow;

    public RecordBackfillController(
            final Client client,
            final RecordBackfillFlow recordBackfillFlow) {
        this.client = client;
        this.recordBackfillFlow = recordBackfillFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startRecordBackfill(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                recordBackfillFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/set-batch-size")
    ResponseEntity<String> setBatchSize(
            @RequestParam final String workflowId,
            @RequestParam final int batchSize) {
        final RecordBackfillFlow stub = client.newRpcStub(RecordBackfillFlow.class, workflowId);
        client.invokeRPC(stub::setBatchSize, batchSize);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/stop")
    ResponseEntity<String> requestStop(@RequestParam final String workflowId) {
        final RecordBackfillFlow stub = client.newRpcStub(RecordBackfillFlow.class, workflowId);
        client.invokeRPC(stub::requestStop);
        return ResponseEntity.ok("done");
    }
}
