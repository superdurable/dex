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

package io.superdurable.dex.integ.basic;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;

public class ModelInputWorkflowState1 implements WorkflowState<io.superdurable.dex.gen.models.Context> {

    @Override
    public Class<io.superdurable.dex.gen.models.Context> getInputType() {
        return io.superdurable.dex.gen.models.Context.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final io.superdurable.dex.gen.models.Context input, Persistence persistence, final Communication communication) {
        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(final Context context, final io.superdurable.dex.gen.models.Context input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        return StateDecision.gracefulCompleteWorkflow(1);
    }
}

