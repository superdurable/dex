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

package io.superdurable.dex.integ.stateapifail;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.RetryPolicy;

public class StateFailBasic implements WorkflowState<Integer> {

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public CommandRequest waitUntil(
            Context context,
            Integer input,
            Persistence persistence,
            final Communication communication) {
        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        throw new RuntimeException("test api failing");
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions().setExecuteApiRetryPolicy(
                new RetryPolicy()
                        .maximumAttempts(1)
                        .backoffCoefficient(2f)
        );
    }
}
