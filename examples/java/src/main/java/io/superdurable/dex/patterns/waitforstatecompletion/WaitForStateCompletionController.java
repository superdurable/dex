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

package io.superdurable.dex.patterns.waitforstatecompletion;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.Client;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;

@RestController
@RequestMapping("/patterns/wait-for-state-completion")
public class WaitForStateCompletionController {
    private final Client client;
    private final WaitForStateCompletionFlow waitForStateCompletionFlow;

    public WaitForStateCompletionController(
            final Client client,
            final WaitForStateCompletionFlow waitForStateCompletionFlow) {
        this.client = client;
        this.waitForStateCompletionFlow = waitForStateCompletionFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startWaitForStateCompletion(@RequestParam final String workflowId)
            throws JsonProcessingException {
        final ObjectMapper objectMapper = new ObjectMapper();
        final JobSeekerData data = new JobSeekerData(1);
        client.startFlow(
                waitForStateCompletionFlow,
                workflowId,
                data,
                ExampleFlows.startOptions());
        client.waitForStepCompletion(
                workflowId,
                StepExecutionId.of("PersistData"),
                Duration.ofMinutes(5));
        final WaitForStateCompletionFlow stub =
                client.newRpcStub(WaitForStateCompletionFlow.class, workflowId);
        final JobSeekerData persistedData = client.invokeRPC(stub::getJobSeekerData);
        return ResponseEntity.ok(String.format(
                "success for workflow %s with data %s",
                workflowId,
                objectMapper.writeValueAsString(persistedData)));
    }
}
