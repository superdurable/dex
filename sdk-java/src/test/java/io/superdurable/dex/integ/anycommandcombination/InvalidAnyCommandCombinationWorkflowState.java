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

package io.superdurable.dex.integ.anycommandcombination;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.SignalCommand;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.RetryPolicy;

import java.time.Duration;
import java.util.Arrays;
import java.util.Collections;

import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_1;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_2;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_3;

public class InvalidAnyCommandCombinationWorkflowState implements io.superdurable.dex.core.WorkflowState<Integer> {
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
                Collections.singletonList(
                        Arrays.asList(SIGNAL_COMMAND_ID_1, SIGNAL_COMMAND_ID_3, TIMER_COMMAND_ID)
                ),
                SignalCommand.create(SIGNAL_COMMAND_ID_1, SIGNAL_CHANNEL_NAME_1),
                SignalCommand.create(SIGNAL_COMMAND_ID_1, SIGNAL_CHANNEL_NAME_2),
                SignalCommand.create(SIGNAL_COMMAND_ID_2, SIGNAL_CHANNEL_NAME_3),
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
