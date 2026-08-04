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
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.command.TimerCommandResult;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.SignalCommand;
import io.superdurable.dex.core.communication.SignalCommandResult;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.ChannelRequestStatus;
import io.superdurable.dex.gen.models.TimerStatus;

import java.time.Duration;
import java.util.Arrays;

import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_1;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_2;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_3;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_PREFIX_1;

public class BasicSignalWorkflowState2 implements WorkflowState<Integer> {
    public static final String SIGNAL_COMMAND_ID_1 = "test-signal-1";
    public static final String SIGNAL_COMMAND_ID_2 = "test-signal-2";
    public static final String SIGNAL_COMMAND_ID_3 = "test-signal-3";
    public static final String TIMER_COMMAND_ID = "test-timer-id";

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
                        Arrays.asList(SIGNAL_COMMAND_ID_1, SIGNAL_COMMAND_ID_3, TIMER_COMMAND_ID)
                ),
                SignalCommand.create(SIGNAL_COMMAND_ID_1, SIGNAL_CHANNEL_NAME_1),
                SignalCommand.create(SIGNAL_COMMAND_ID_1, SIGNAL_CHANNEL_NAME_2),
                SignalCommand.create(SIGNAL_COMMAND_ID_2, SIGNAL_CHANNEL_NAME_3),
                SignalCommand.create(SIGNAL_COMMAND_ID_3, SIGNAL_CHANNEL_PREFIX_1 + "1"),
                TimerCommand.createByDuration(TIMER_COMMAND_ID, Duration.ofDays(365))
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

        SignalCommandResult signalCommandResult3 = commandResults.getAllSignalCommandResults().get(2);
        if (signalCommandResult3.getSignalRequestStatusEnum() != ChannelRequestStatus.RECEIVED || !signalCommandResult3.getCommandId().equals(SIGNAL_COMMAND_ID_2)) {
            throw new RuntimeException("the 3 signal should be received");
        }

        SignalCommandResult signalCommandResult4 = commandResults.getAllSignalCommandResults().get(3);
        if (signalCommandResult4.getSignalRequestStatusEnum() != ChannelRequestStatus.RECEIVED || !signalCommandResult4.getCommandId().equals(SIGNAL_COMMAND_ID_3)) {
            throw new RuntimeException("the 4 signal created by prefix should be received");
        }

        final TimerCommandResult timerResult = commandResults.getAllTimerCommandResults().get(0);
        if (timerResult.getTimerStatus() != TimerStatus.FIRED) {
            throw new RuntimeException("the timer should be fired");
        }
        return StateDecision.gracefulCompleteWorkflow(output);
    }
}
