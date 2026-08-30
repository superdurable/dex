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

package io.superdurable.dex.products.jobpost;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.StartFlowOptions;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/products/job-post")
public class JobPostingController {
    private final Client client;
    private final JobPostingFlow flow;

    public JobPostingController(
            final Client client,
            final JobPostingFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/create")
    public ResponseEntity<String> create(
            @RequestParam String title,
            @RequestParam String description) {
        final String flowId = "job_id_" + System.currentTimeMillis() / 1000;
        title = escapeQuote(title);
        description = escapeQuote(description);

        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofHours(24))
                .addAttribute(flow.title, title)
                .addAttribute(flow.jobDescription, description)
                .addAttribute(flow.lastUpdateTimeMillis, System.currentTimeMillis())
                .configOverride(FlowConfig.newBuilder().continueAsNewThreshold(10).build())
                .build();
        client.startFlow(flow, flowId, null, options);
        return ResponseEntity.ok(String.format("started workflowId: %s", flowId));
    }

    @GetMapping("/read")
    public ResponseEntity<JobInfo> read(@RequestParam final String workflowId) {
        final JobPostingFlow stub = client.newRpcStub(JobPostingFlow.class, workflowId);
        return ResponseEntity.ok(client.invokeRPC(stub::get));
    }

    @GetMapping("/update")
    public ResponseEntity<String> update(
            @RequestParam final String workflowId,
            @RequestParam String title,
            @RequestParam String description,
            @RequestParam(defaultValue = "test-notes") String notes) {
        title = escapeQuote(title);
        description = escapeQuote(description);
        notes = escapeQuote(notes);

        final JobPostingFlow stub = client.newRpcStub(JobPostingFlow.class, workflowId);
        client.invokeRPC(stub::update, new JobInfo(title, description, notes));
        return ResponseEntity.ok("updated");
    }

    @GetMapping("/delete")
    public ResponseEntity<String> delete(@RequestParam final String workflowId) {
        client.stopFlow(workflowId);
        return ResponseEntity.ok(
                "marked as soft deleted, will be delete later after retention");
    }

    @GetMapping("/search")
    public ResponseEntity<Map<String, String>> search(@RequestParam String query) {
        query = escapeQuote(query);
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put(
                "message",
                "Java Client 0.0.3 does not expose SearchFlows; "
                        + "Title and JobDescription are FULL_TEXT AttributeIndexes "
                        + "for when SearchFlows is available.");
        response.put("query", query);
        return ResponseEntity.ok(response);
    }

    private static String escapeQuote(String input) {
        if (input.startsWith("'")) {
            input = input.substring(1, input.length() - 1);
        }
        if (input.startsWith("\"")) {
            input = input.substring(1, input.length() - 1);
        }
        return input;
    }
}
