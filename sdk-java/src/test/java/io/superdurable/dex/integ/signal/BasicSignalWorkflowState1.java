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

package io.superdurable.dex.integ.signal;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.SignalCommand;
import io.superdurable.dex.core.communication.SignalCommandResult;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.ChannelRequestStatus;

import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_1;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_2;

public class BasicSignalWorkflowState1 implements WorkflowState<Integer> {
    public static final String COMMAND_ID = "test-signal-id";
    
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
        return CommandRequest.forAnyCommandCompleted(
                SignalCommand.create(COMMAND_ID, SIGNAL_CHANNEL_NAME_1),
                SignalCommand.create(COMMAND_ID, SIGNAL_CHANNEL_NAME_2)
        );
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        SignalCommandResult signalCommandResult = commandResults.getAllSignalCommandResults().get(0);
        Integer output = input + (Integer) signalCommandResult.getSignalValue().get();

        SignalCommandResult signalCommandResult2 = commandResults.getAllSignalCommandResults().get(1);
        if (signalCommandResult2.getSignalRequestStatusEnum() != ChannelRequestStatus.WAITING) {
            throw new RuntimeException("the second signal should be waiting");
        }
        return StateDecision.singleNextState(BasicSignalWorkflowState2.class, output);
    }
}
