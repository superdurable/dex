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

import io.superdurable.dex.core.*;
import io.superdurable.dex.core.exceptions.LongPollTimeoutException;
import io.superdurable.dex.gen.models.ErrorSubStatus;
import io.superdurable.dex.gen.models.WorkflowErrorType;
import io.superdurable.dex.gen.models.WorkflowStatus;
import io.superdurable.dex.gen.models.WorkflowStopType;
import io.superdurable.dex.integ.forcefail.ForceFailWorkflow;
import io.superdurable.dex.integ.signal.BasicSignalWorkflow;
import io.superdurable.dex.integ.stateapifail.WorkflowBasicStateFail;
import io.superdurable.dex.integ.stateapitimeout.StateApiTimeoutFailWorkflow;
import io.superdurable.dex.integ.statedecision.EmptyStateDecisionWorkflow;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;

import static io.superdurable.dex.gen.models.WorkflowErrorType.CLIENT_API_FAILING_WORKFLOW_ERROR_TYPE;

public class WorkflowUncompletedTest {

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    public void testWorkflowWaitTimeout() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testWorkflowTimeout" + System.currentTimeMillis() / 1000;
        final Integer input = 1;

        final String runId = client.startWorkflow(BasicSignalWorkflow.class, wfId, 100, input);

        Assertions.assertThrows(WorkflowUncompletedException.class, () -> client.tryGettingSimpleWorkflowResult(Integer.class, wfId));
        Assertions.assertThrows(WorkflowUncompletedException.class, () -> client.tryGettingSimpleWorkflowResult(Integer.class, wfId, runId));
        Assertions.assertThrows(WorkflowUncompletedException.class, () -> client.tryGettingComplexWorkflowResult(wfId));
        Assertions.assertThrows(WorkflowUncompletedException.class, () -> client.tryGettingComplexWorkflowResult(wfId, runId));

        long startMs = System.currentTimeMillis();
        long elapsedMs;
        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (LongPollTimeoutException e) {
            Assertions.assertEquals(ErrorSubStatus.LONG_POLL_TIME_OUT_SUB_STATUS, e.getErrorSubStatus());
            Assertions.assertEquals(420, e.getStatusCode());

            elapsedMs = (System.currentTimeMillis() - startMs) / 1000;
            // NOTE: because the default poll timeout is 60-2 = 28s in Dex service
            Assertions.assertTrue(elapsedMs >= 55 && elapsedMs <= 62, "expect to poll for 58 seconds, actual is %d " + elapsedMs);
            return;
        } catch (Exception e) {
            Assertions.fail("expected to catch LongPollTimeoutException");
        }
        Assertions.fail("expected to catch LongPollTimeoutException");
    }

    @Test
    public void testWorkflowTimeout() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testWorkflowTimeout" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                BasicSignalWorkflow.class, wfId, 1, input);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.TIMEOUT, e.getClosedStatus());
            Assertions.assertNull(e.getErrorSubType());
            Assertions.assertNull(e.getErrorMessage());
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testWorkflowCanceled() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testWorkflowTimeout" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                BasicSignalWorkflow.class, wfId, 10, input);

        client.stopWorkflow(wfId);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.CANCELED, e.getClosedStatus());
            Assertions.assertNull(e.getErrorSubType());
            Assertions.assertNull(e.getErrorMessage());
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testWorkflowCanceledWhenNotProvidingRunId() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testWorkflowTimeout" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                BasicSignalWorkflow.class, wfId, 10, input);

        client.stopWorkflow(wfId);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.CANCELED, e.getClosedStatus());
            Assertions.assertNull(e.getErrorSubType());
            Assertions.assertNull(e.getErrorMessage());
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testWorkflowTerminated() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testWorkflowTerminated" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                BasicSignalWorkflow.class, wfId, 10, input);

        client.stopWorkflow(wfId, "", ImmutableStopWorkflowOptions.builder().workflowStopType(WorkflowStopType.TERMINATE).build());

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.TERMINATED, e.getClosedStatus());
            Assertions.assertNull(e.getErrorSubType());
            Assertions.assertNull(e.getErrorMessage());
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testWorkflowFailByAPI() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "testWorkflowTerminated" + System.currentTimeMillis() / 1000;
        final Integer input = 1;
        final String runId = client.startWorkflow(
                BasicSignalWorkflow.class, wfId, 10, input);

        client.stopWorkflow(wfId, "", ImmutableStopWorkflowOptions.builder().workflowStopType(WorkflowStopType.FAIL).reason("fail by API").build());

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.FAILED, e.getClosedStatus());
            Assertions.assertEquals(CLIENT_API_FAILING_WORKFLOW_ERROR_TYPE, e.getErrorSubType());
            Assertions.assertEquals("fail by API", e.getErrorMessage());
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testForceFailWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testForceFailWorkflow" + startTs / 1000;
        final Integer input = 5;

        final String runId = client.startWorkflow(
                ForceFailWorkflow.class, wfId, 10, input);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.FAILED, e.getClosedStatus());
            Assertions.assertEquals(WorkflowErrorType.STATE_DECISION_FAILING_WORKFLOW_ERROR_TYPE, e.getErrorSubType());
            Assertions.assertNull(e.getErrorMessage());
            Assertions.assertEquals(1, e.getStateResultsSize());
            String out = e.getStateResult(0, String.class);
            Assertions.assertEquals("a failing message", out);
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testStateApiFailWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testStateApiFailWorkflow" + startTs / 1000;
        final Integer input = 5;

        final String runId = client.startWorkflow(
                WorkflowBasicStateFail.class, wfId, 10, input);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.FAILED, e.getClosedStatus());
            Assertions.assertEquals(WorkflowErrorType.STATE_API_FAIL_ERROR_TYPE, e.getErrorSubType());
            Assertions.assertTrue(e.getErrorMessage().contains("test api failing"));
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testStateApiTimeoutWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testStateApiTimeoutWorkflow" + startTs / 1000;
        final Integer input = 5;

        final String runId = client.startWorkflow(
                StateApiTimeoutFailWorkflow.class, wfId, 10, input);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.FAILED, e.getClosedStatus());
            Assertions.assertEquals(WorkflowErrorType.STATE_API_FAIL_ERROR_TYPE, e.getErrorSubType());
            Assertions.assertTrue(
                    e.getErrorMessage().contains("activity StartToClose timeout"),
                    e.getErrorMessage()
            );
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }

    @Test
    public void testEmptyStateDecisionTimeoutWorkflow() throws InterruptedException {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testEmptyStateDecisionTimeoutWorkflow" + startTs / 1000;

        final String runId = client.startWorkflow(
                EmptyStateDecisionWorkflow.class, wfId, 10, null);

        try {
            client.getSimpleWorkflowResultWithWait(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.FAILED, e.getClosedStatus());
            Assertions.assertEquals(WorkflowErrorType.STATE_API_FAIL_ERROR_TYPE, e.getErrorSubType());
            Assertions.assertTrue(
                    e.getErrorMessage().contains("State decision returned by execute method cannot be null or empty")
            );
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }
}
