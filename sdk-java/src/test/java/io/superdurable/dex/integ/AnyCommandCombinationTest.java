/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ClientOptions;
import io.superdurable.dex.core.WorkflowUncompletedException;
import io.superdurable.dex.gen.models.WorkflowErrorType;
import io.superdurable.dex.gen.models.WorkflowStatus;
import io.superdurable.dex.integ.anycommandcombination.AnyCommandCombinationFailWorkflow;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;

class AnyCommandCombinationTest {

    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }

    @Test
    void testStateApiFailWorkflow() {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final long startTs = System.currentTimeMillis();
        final String wfId = "testStateApiFailWorkflow" + startTs / 1000;
        final Integer input = 5;

        final String runId = client.startWorkflow(
                AnyCommandCombinationFailWorkflow.class, wfId, 10, input);

        try {
            client.waitForWorkflowCompletion(Integer.class, wfId);
        } catch (WorkflowUncompletedException e) {
            Assertions.assertEquals(runId, e.getRunId());
            Assertions.assertEquals(WorkflowStatus.FAILED, e.getClosedStatus());
            Assertions.assertEquals(WorkflowErrorType.STATE_API_FAIL_ERROR_TYPE, e.getErrorSubType());
            Assertions.assertTrue(e.getErrorMessage().contains("CommandNotFoundException: Found unknown commandId in the combination list"));
            Assertions.assertEquals(0, e.getStateResultsSize());
            return;
        }
        Assertions.fail("no exception caught");
    }
}
