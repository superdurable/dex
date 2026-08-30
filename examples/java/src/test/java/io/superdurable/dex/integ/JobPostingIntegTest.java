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

package io.superdurable.dex.integ;

import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.products.jobpost.JobInfo;
import io.superdurable.dex.products.jobpost.JobPostingFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;

@ExtendWith(SharedIntegExtension.class)
public class JobPostingIntegTest {
    @Test
    void jobPostingUpdateReachesBothJobBoards() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final JobPostingFlow flow = environment.jobPostingFlow();
        final String flowId = environment.newFlowId("job-posting");
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofHours(1))
                .addAttribute(flow.title, "Software Engineer")
                .addAttribute(flow.jobDescription, "Build reliable systems")
                .addAttribute(flow.lastUpdateTimeMillis, System.currentTimeMillis())
                .build();
        environment.client().startFlow(flow, flowId, null, options);

        final JobPostingFlow stub = environment.client().newRpcStub(JobPostingFlow.class, flowId);
        final JobInfo updated = new JobInfo(
                "Senior Software Engineer",
                "Build durable systems",
                "expanded scope");
        environment.client().invokeRPC(stub::update, updated);
        environment.client().waitForStepCompletion(
                flowId,
                StepExecutionId.of("UpdateLinkedInPosting"),
                Duration.ofSeconds(30));
        environment.client().waitForStepCompletion(
                flowId,
                StepExecutionId.of("UpdateIndeedPosting"),
                Duration.ofSeconds(30));

        final JobInfo actual = environment.client().invokeRPC(stub::get);
        assertEquals(updated.title, actual.title);
        assertEquals(updated.description, actual.description);
        assertEquals(updated.notes, actual.notes);
        environment.client().stopFlow(flowId);
    }
}
