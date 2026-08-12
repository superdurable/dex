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

package io.superdurable.dex.integ;

import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.workflow.retryingfailure.RetryingFailureFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;

@ExtendWith(SharedIntegExtension.class)
public class RetryingFailureIntegTest {
    @Test
    void executeFailureRemainsActiveForItsLongRetry() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final RetryingFailureFlow flow = environment.retryingFailureFlow();
        final String flowId = environment.newFlowId("retrying-failure");

        final String runId = environment.client().startFlow(
                flow,
                flowId,
                null,
                environment.startOptions());
        assertNotNull(runId);
        assertFalse(runId.isEmpty());

        try {
            assertThrows(
                    LongPollTimeoutException.class,
                    () -> environment.client().waitForFlow(flowId, Duration.ofSeconds(1)).getSingleOutput(Void.class));
        } finally {
            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.TERMINATE, "integration test cleanup"));
        }
    }
}
