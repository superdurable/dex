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

package io.superdurable.dex.integ.rpc;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.core.persistence.Persistence;

import static io.superdurable.dex.integ.rpc.RpcWorkflow.INTERNAL_CHANNEL_NAME;

public class RpcLockingWorkflowState2 implements WorkflowState<Void> {

    private static int counter = 0;

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(
            Context context,
            Void input,
            Persistence persistence,
            final Communication communication) {
        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(
            Context context,
            Void input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        counter++;
        return StateDecision.gracefulCompleteWorkflow("The execute method was executed " + counter + " times");

    }

    // reset counter so that new test can use it
    public static int resetCounter() {
        final int old = counter;
        counter = 0;
        return old;
    }
}
