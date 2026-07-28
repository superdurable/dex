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
import io.iworkflow.gen.models.WorkflowSearchResponse;
import io.iworkflow.workflow.engagement.EngagementWorkflow;
import io.iworkflow.workflow.engagement.model.EngagementDescription;
import io.iworkflow.workflow.engagement.model.EngagementInput;
import io.iworkflow.workflow.engagement.model.ImmutableEngagementInput;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

@Controller
@RequestMapping("/engagement")
public class EngagementWorkflowController {

    private final Client client;

    public EngagementWorkflowController(
            final Client client
    ) {
        this.client = client;
    }

    @GetMapping("/start")
    public ResponseEntity<String> start(
            @RequestParam(defaultValue = "test-employer") String employerId,
            @RequestParam(defaultValue = "test-jobseeker") String jobSeekerId,
            @RequestParam(defaultValue = "test-notes") String notes
    ) {
        final String wfId = "engagement_test_id_" + System.currentTimeMillis() / 1000;
        final EngagementInput input = ImmutableEngagementInput.builder()
                .employerId(employerId)
                .jobSeekerId(jobSeekerId)
                .notes(notes)
                .build();
        client.startWorkflow(EngagementWorkflow.class, wfId, 3600, input);

        return ResponseEntity.ok(String.format("started workflowId: %s", wfId));
    }

    @GetMapping("/optout")
    public ResponseEntity<String> optout(
            @RequestParam String workflowId
    ) {
        client.signalWorkflow(EngagementWorkflow.class, workflowId, EngagementWorkflow.SIGNAL_NAME_OPT_OUT_REMINDER, null);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/decline")
    public ResponseEntity<String> decline(
            @RequestParam String workflowId,
            @RequestParam String notes
    ) {
        final EngagementWorkflow rpcStub = client.newRpcStub(EngagementWorkflow.class, workflowId);
        client.invokeRPC(rpcStub::decline, notes);

        return ResponseEntity.ok("declined");
    }

    @GetMapping("/describe")
    public ResponseEntity<EngagementDescription> describe(
            @RequestParam String workflowId
    ) {
        final EngagementWorkflow rpcStub = client.newRpcStub(EngagementWorkflow.class, workflowId);
        final EngagementDescription description = client.invokeRPC(rpcStub::describe);

        return ResponseEntity.ok(description);
    }

    @GetMapping("/accept")
    public ResponseEntity<String> accept(
            @RequestParam String workflowId,
            @RequestParam String notes
    ) {
        final EngagementWorkflow rpcStub = client.newRpcStub(EngagementWorkflow.class, workflowId);
        client.invokeRPC(rpcStub::accept, notes);

        return ResponseEntity.ok("accepted");
    }

    @GetMapping("/list")
    public ResponseEntity<WorkflowSearchResponse> list(
            @RequestParam String query
    ) {
        if (query.startsWith("'")) {
            query = query.substring(1, query.length() - 1);
        }
        System.out.println("got query for search: " + query);
        // this is just a shortcut for demo for how flexible the search can be
        // in real world you may want to provide some search patterns like listByEmployerId+status etc
        WorkflowSearchResponse response = client.searchWorkflow(query, 1000);

        return ResponseEntity.ok(response);
    }
}