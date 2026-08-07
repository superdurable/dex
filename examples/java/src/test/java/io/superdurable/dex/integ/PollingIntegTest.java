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

import io.superdurable.dex.workflow.polling.PollingFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

@ExtendWith(SharedIntegExtension.class)
public class PollingIntegTest {
    @Test
    void pollingStartAndChannels() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final PollingFlow flow = environment.pollingFlow();
        final String flowId = environment.newFlowId("polling");

        final String runId = environment.client().startFlow(
                flow,
                flowId,
                1,
                environment.startOptions());
        assertNotNull(runId);
        assertFalse(runId.isEmpty());

        environment.client().publish(flowId, flow.taskACompleted, (Void) null);
        environment.client().publish(flowId, flow.taskBCompleted, (Void) null);

        final String output = environment.client().waitForFlow(
                flowId,
                String.class,
                Duration.ofSeconds(45));
        assertEquals("all tasks completed", output);

        final Integer pollCount = environment.client().getAttribute(flowId, flow.currentPolls);
        assertEquals(1, pollCount);
    }
}
