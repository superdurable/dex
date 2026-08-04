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
import io.superdurable.dex.gen.models.ChannelRequestStatus;

import java.util.Arrays;

public class BasicInternalChannelWorkflowState1 implements WorkflowState<Integer> {
    public static final String COMMAND_ID = "test-cmd-id";
    public static final String COMMAND_ID_2 = "test-cmd-id-2";

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
        return CommandRequest.forAnyCommandCombinationCompleted(
                Arrays.asList(
                        Arrays.asList(COMMAND_ID, COMMAND_ID_2)
                ),
                InternalChannelCommand.create(COMMAND_ID, BasicInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME_1),
                InternalChannelCommand.create(COMMAND_ID, BasicInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME_2),
                InternalChannelCommand.create(COMMAND_ID_2, BasicInternalChannelWorkflow.INTER_STATE_CHANNEL_PREFIX_1 + "1")
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
        Integer output = input + (Integer) result1.getValue().get();

        final InternalChannelCommandResult result2 = commandResults.getAllInternalChannelCommandResult().get(1);
        if (result2.getRequestStatusEnum() != ChannelRequestStatus.WAITING) {
            throw new RuntimeException("the second command should be waiting");
        }

        final InternalChannelCommandResult result3 = commandResults.getAllInternalChannelCommandResult().get(2);
        if (result3.getRequestStatusEnum() != ChannelRequestStatus.RECEIVED) {
            throw new RuntimeException("the third command should be received");
        }

        return StateDecision.gracefulCompleteWorkflow(output);
    }
}
