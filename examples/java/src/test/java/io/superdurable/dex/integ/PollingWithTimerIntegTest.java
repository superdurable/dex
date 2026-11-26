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

import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.TimerId;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.patterns.pollingwithtimer.PollingWithTimerFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.NoSuchElementException;
import java.util.function.BooleanSupplier;

import static org.junit.jupiter.api.Assertions.assertEquals;

@ExtendWith(SharedIntegExtension.class)
public class PollingWithTimerIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    @Test
    void pollingWithTimerChecksTheJobWhenTheIntervalTimerFires() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId("pattern-polling-with-timer");
        environment.client().startFlow(
                environment.pollingWithTimerFlow(),
                flowId,
                null,
                environment.startOptions());

        awaitCondition(environment, () -> {
            try {
                environment.fakeJobService().completeJob(flowId);
                return true;
            } catch (final NoSuchElementException jobNotStarted) {
                return false;
            }
        }, flowId + " job was never started");

        awaitCondition(environment, () -> {
            try {
                environment.client().skipTimer(
                        flowId,
                        StepExecutionId.of("CheckJobStatus", 1),
                        TimerId.byConditionId(PollingWithTimerFlow.POLL_INTERVAL_TIMER_ID));
                return true;
            } catch (final DexServiceException notWaiting) {
                return false;
            }
        }, flowId + " first interval Timer was never skipped");

        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals("artifact for " + flowId, result.getSingleOutput(String.class));
    }

    private static void awaitCondition(
            final IntegEnvironment environment,
            final BooleanSupplier condition,
            final String message) {
        try {
            environment.awaitCondition(
                    condition::getAsBoolean, Boolean::booleanValue, WAIT_TIMEOUT, message);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted waiting: " + message, interrupted);
        }
    }
}
