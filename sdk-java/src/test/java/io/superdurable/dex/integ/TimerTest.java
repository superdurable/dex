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

import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ClientOptions;
import io.superdurable.dex.core.WorkflowOptions;
import io.superdurable.dex.integ.timer.BasicTimerWorkflow;
import io.superdurable.dex.integ.timer.BasicTimerWorkflowState1;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;

public class TimerTest {

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testBasicTimerWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "basic-timer-test-id" + startTs / 1000;
        final Integer input = 5;

        client.startWorkflow(
                BasicTimerWorkflow.class, wfId, 10, input,
                WorkflowOptions.extendedBuilder()
                        .waitForCompletionStates(BasicTimerWorkflowState1.class)
                        .getBuilder().build());

        client.waitForStateExecutionCompletion(wfId, BasicTimerWorkflowState1.class);
        client.waitForWorkflowCompletion(wfId);
        final long elapsed = System.currentTimeMillis() - startTs;
        Assertions.assertTrue(elapsed >= 4000 && elapsed <= 7000, String.format("actual duration: %d", elapsed));
    }
}
