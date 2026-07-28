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

package io.iworkflow.controller;

import io.iworkflow.core.Client;
import io.iworkflow.core.exceptions.WorkflowAlreadyStartedException;
import io.iworkflow.workflow.microservices.OrchestrationWorkflow;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

@Controller
@RequestMapping("/microservice")
public class MicroserviceWorkflowController {

    private final Client client;

    public MicroserviceWorkflowController(
            final Client client
    ) {
        this.client = client;
    }

    @GetMapping("/start")
    public ResponseEntity<String> start(
            @RequestParam String workflowId
    ) {
        try {
            client.startWorkflow(OrchestrationWorkflow.class, workflowId, 3600, "some input data, could be any object rather than a string");
        } catch (WorkflowAlreadyStartedException e) {
            // ignore
        }
        return ResponseEntity.ok("success");
    }

    @GetMapping("/signal")
    ResponseEntity<String> receiveSignalForApiOrchestration(
            @RequestParam String workflowId) {
        client.signalWorkflow(OrchestrationWorkflow.class, workflowId, "", OrchestrationWorkflow.READY_SIGNAL, null);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/swap")
    ResponseEntity<String> swapData(
            @RequestParam String workflowId,
            @RequestParam String data) {
        final OrchestrationWorkflow rpcStub = client.newRpcStub(OrchestrationWorkflow.class, workflowId);
        String oldData = client.invokeRPC(rpcStub::swap, data);
        return ResponseEntity.ok("oldData: " + oldData);
    }
}