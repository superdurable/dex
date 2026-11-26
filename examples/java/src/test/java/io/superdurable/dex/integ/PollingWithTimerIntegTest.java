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
import io.superdurable.dex.patterns.polling.FakeJobService;
import io.superdurable.dex.patterns.pollingwithtimer.PollingWithTimerFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.NoSuchElementException;
import java.util.function.BooleanSupplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

@ExtendWith(SharedIntegExtension.class)
public class PollingWithTimerIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    // Mirrors the example's round limit.
    private static final int MAX_POLL_ROUNDS = 60;

    @Test
    void pollingWithTimerMovesToTheNextRoundWhileTheJobRuns() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final FakeJobService jobs = environment.fakeJobService();
        final String flowId = environment.newFlowId("pattern-polling-with-timer");
        jobs.keepJobRunning(flowId);
        startFlow(environment, flowId);

        skipPollIntervalTimer(environment, flowId, 1);
        awaitCondition(environment, () -> jobs.countStatusChecks(flowId) == 1,
                flowId + " round 1 never checked the running job");
        jobs.completeJob(flowId);
        skipPollIntervalTimer(environment, flowId, 2);

        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        assertEquals("artifact for " + flowId, result.getSingleOutput(String.class));
        assertEquals(2, jobs.countStatusChecks(flowId));
    }

    @Test
    void pollingWithTimerFailsAfterTheMaximumRounds() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final FakeJobService jobs = environment.fakeJobService();
        final String flowId = environment.newFlowId("pattern-polling-with-timer-rounds");
        jobs.keepJobRunning(flowId);
        startFlow(environment, flowId);

        for (int round = 1; round <= MAX_POLL_ROUNDS; round++) {
            skipPollIntervalTimer(environment, flowId, round);
        }

        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.FAILED, result.getStatus(), result.getErrorMessage());
        assertTrue(result.getErrorMessage().contains("did not finish in " + MAX_POLL_ROUNDS + " rounds"),
                result.getErrorMessage());
        assertEquals(MAX_POLL_ROUNDS, jobs.countStatusChecks(flowId));
    }

    @Test
    void pollingWithTimerRetriesATransientStatusFailure() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final FakeJobService jobs = environment.fakeJobService();
        final String flowId = environment.newFlowId("pattern-polling-with-timer-retry");
        jobs.failNextStatusChecks(flowId, 1);
        startFlow(environment, flowId);
        completeJobWhenStarted(environment, flowId);

        skipPollIntervalTimer(environment, flowId, 1);
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), result.getErrorMessage());
        assertEquals(2, jobs.countStatusChecks(flowId));
    }

    @Test
    void pollingWithTimerRecordsTheFailureWhenRetriesRunOut() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final FakeJobService jobs = environment.fakeJobService();
        final String flowId = environment.newFlowId("pattern-polling-with-timer-recovery");
        jobs.failNextStatusChecks(flowId, 3);
        startFlow(environment, flowId);
        completeJobWhenStarted(environment, flowId);

        skipPollIntervalTimer(environment, flowId, 1);
        final FlowResult result = environment.client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.FAILED, result.getStatus(), result.getErrorMessage());
        assertTrue(result.getErrorMessage().contains("checking job " + flowId + " failed"),
                result.getErrorMessage());
        assertEquals(3, jobs.countStatusChecks(flowId));
    }

    private static void startFlow(final IntegEnvironment environment, final String flowId) {
        environment.client().startFlow(
                environment.pollingWithTimerFlow(),
                flowId,
                null,
                environment.startOptions());
    }

    private static void completeJobWhenStarted(final IntegEnvironment environment, final String flowId) {
        awaitCondition(environment, () -> {
            try {
                environment.fakeJobService().completeJob(flowId);
                return true;
            } catch (final NoSuchElementException jobNotStarted) {
                return false;
            }
        }, flowId + " job was never started");
    }

    private static void skipPollIntervalTimer(
            final IntegEnvironment environment, final String flowId, final int round) {
        awaitCondition(environment, () -> {
            try {
                environment.client().skipTimer(
                        flowId,
                        StepExecutionId.of("CheckJobStatus", round),
                        TimerId.byConditionId(PollingWithTimerFlow.POLL_INTERVAL_TIMER_ID));
                return true;
            } catch (final DexServiceException notWaiting) {
                return false;
            }
        }, flowId + " interval Timer of round " + round + " was never skipped");
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
