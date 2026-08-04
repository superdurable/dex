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
import io.superdurable.dex.core.ImmutableWorkflowOptions;
import io.superdurable.dex.core.WorkflowOptions;
import io.superdurable.dex.gen.models.IDReusePolicy;
import io.superdurable.dex.integ.stateoptions.StateOptionsWorkflow;
import io.superdurable.dex.spring.TestSingletonWorkerService;
import io.superdurable.dex.spring.controller.WorkflowRegistry;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;

public class StateOptionsTest {
    @BeforeEach
    public void setup() throws ExecutionException, InterruptedException {
        TestSingletonWorkerService.startWorkerIfNotUp();
    }
    @Test
    public void testStateOptionsWorkflow() {
        final Client client = new Client(WorkflowRegistry.registry, ClientOptions.localDefault);
        final String wfId = "state-options-test-id" + System.currentTimeMillis() / 1000;
        final WorkflowOptions startOptions = ImmutableWorkflowOptions.builder()
                .workflowIdReusePolicy(IDReusePolicy.DISALLOW_REUSE)
                .build();
        client.startWorkflow(StateOptionsWorkflow.class, wfId, 10, null, startOptions);
        // wait for workflow to finish
        final String output = client.getSimpleWorkflowResultWithWait(String.class, wfId);

        // If no exception thrown, then the test is successful
        Assertions.assertEquals("success", output);
    }
}

