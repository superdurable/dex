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

package io.superdurable.dex.integ.timer;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;

import java.time.Duration;

public class BasicTimerWorkflowState1 implements WorkflowState<Integer> {
    public static final String COMMAND_ID = "test-timer-id";

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
        return CommandRequest.forAllCommandCompleted(TimerCommand.createByDuration(Duration.ofSeconds(input)));
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        return StateDecision.gracefulCompleteWorkflow();
    }
}
