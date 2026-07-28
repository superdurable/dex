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

import com.google.common.collect.ImmutableMap;
import io.iworkflow.core.Client;
import io.iworkflow.core.ImmutableWorkflowOptions;
import io.iworkflow.gen.models.WorkflowConfig;
import io.iworkflow.gen.models.WorkflowSearchResponse;
import io.iworkflow.workflow.jobpost.ImmutableJobInfo;
import io.iworkflow.workflow.jobpost.JobInfo;
import io.iworkflow.workflow.jobpost.JobPostWorkflow;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

@Controller
@RequestMapping("/jobpost")
public class JobPostController {

    private final Client client;

    public JobPostController(
            final Client client
    ) {
        this.client = client;
    }

    @GetMapping("/create")
    public ResponseEntity<String> create(
            @RequestParam String title,
            @RequestParam String description
    ) {
        final String wfId = "job_id_" + System.currentTimeMillis() / 1000;

        title = escapeQuote(title);
        description = escapeQuote(description);

        client.startWorkflow(JobPostWorkflow.class, wfId, 3600, null,
                ImmutableWorkflowOptions.builder()
                        .initialSearchAttribute(ImmutableMap.of(
                                JobPostWorkflow.SA_KEY_TITLE, title,
                                JobPostWorkflow.SA_KEY_JOB_DESCRIPTION, description,
                                JobPostWorkflow.SA_KEY_LAST_UPDATE_TIMESTAMP, System.currentTimeMillis() / 1000
                        ))
                        .workflowConfigOverride(
                                // set lower threshold to demo auto continueAsNew
                                new WorkflowConfig().continueAsNewThreshold(10)
                        )
                        .build());

        return ResponseEntity.ok(String.format("started workflowId: %s", wfId));
    }

    @GetMapping("/read")
    public ResponseEntity<JobInfo> read(
            @RequestParam String workflowId) {
        final JobPostWorkflow rpcStub = client.newRpcStub(JobPostWorkflow.class, workflowId);

        JobInfo jobInfo = client.invokeRPC(rpcStub::get);

        return ResponseEntity.ok(jobInfo);
    }

    @GetMapping("/update")
    public ResponseEntity<String> update(
            @RequestParam String workflowId,
            @RequestParam String title,
            @RequestParam String description,
            @RequestParam(defaultValue = "test-notes") String notes
    ) {
        title = escapeQuote(title);
        description = escapeQuote(description);
        notes = escapeQuote(notes);

        final JobPostWorkflow rpcStub = client.newRpcStub(JobPostWorkflow.class, workflowId);
        JobInfo input = ImmutableJobInfo.builder()
                .description(description)
                .title(title)
                .notes(notes)
                .build();
        client.invokeRPC(rpcStub::update, input);

        return ResponseEntity.ok("updated");
    }

    @GetMapping("/delete")
    public ResponseEntity<String> delete(
            @RequestParam String workflowId) {
        client.stopWorkflow(workflowId);
        return ResponseEntity.ok("marked as soft deleted, will be delete later after retention");
    }

    @GetMapping("/search")
    public ResponseEntity<WorkflowSearchResponse> search(
            @RequestParam String query
    ) {
        query = escapeQuote(query);
        System.out.println("got query for search: " + query);
        // this is just a shortcut for demo for how flexible the search can be
        // in real world you may want to provide some search patterns like listByEmployerId+status etc
        WorkflowSearchResponse response = client.searchWorkflow(query, 1000);

        return ResponseEntity.ok(response);
    }

    String escapeQuote(String input) {
        if (input.startsWith("'")) {
            input = input.substring(1, input.length() - 1);
        }
        if (input.startsWith("\"")) {
            input = input.substring(1, input.length() - 1);
        }
        return input;
    }
}