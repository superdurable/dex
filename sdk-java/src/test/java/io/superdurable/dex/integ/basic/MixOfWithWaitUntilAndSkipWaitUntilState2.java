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
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;

import java.time.Duration;

import static io.superdurable.dex.integ.basic.MixOfWithWaitUntilAndSkipWaitUntilWorkflow.SHARED_STATE_OPTIONS;

public class MixOfWithWaitUntilAndSkipWaitUntilState2 implements WorkflowState<Integer> {

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public CommandRequest waitUntil(
            final Context context,
            final Integer input,
            Persistence persistence,
            final Communication communication) {
        return CommandRequest.forAllCommandCompleted(TimerCommand.createByDuration(Duration.ofSeconds(1)));
    }

    @Override
    public StateDecision execute(
            final Context context,
            final Integer input,
            final CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        final int output = input + 1;
        commandResults.getAllTimerCommandResults().get(0).getTimerStatus();
        return StateDecision.gracefulCompleteWorkflow(output);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return SHARED_STATE_OPTIONS;
    }
}