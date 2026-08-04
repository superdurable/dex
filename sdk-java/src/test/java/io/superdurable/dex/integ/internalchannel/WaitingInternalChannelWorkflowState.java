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

package io.superdurable.dex.integ.internalchannel;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.core.communication.InternalChannelCommandResult;
import io.superdurable.dex.core.persistence.Persistence;

public class WaitingInternalChannelWorkflowState implements WorkflowState<Integer> {

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
        return CommandRequest.forAllCommandCompleted(
                InternalChannelCommand.create(WaitingInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME),
                InternalChannelCommand.create(WaitingInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME)
        );
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        final InternalChannelCommandResult result1 = commandResults.getAllInternalChannelCommandResult().get(0);
        final InternalChannelCommandResult result2 = commandResults.getAllInternalChannelCommandResult().get(1);

        Integer output = input + (Integer) result1.getValue().get() + (Integer) result2.getValue().get();

        return StateDecision.gracefulCompleteWorkflow(output);
    }
}
