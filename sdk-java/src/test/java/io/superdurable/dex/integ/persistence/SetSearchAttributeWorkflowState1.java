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

package io.superdurable.dex.integ.persistence;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ImmutableStateDecision;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.SignalCommand;
import io.superdurable.dex.core.persistence.Persistence;

import java.util.Arrays;

public class SetSearchAttributeWorkflowState1 implements WorkflowState<String> {
    public static final String STATE_ID = "setSearchAttribute-s1";

    @Override
    public String getStateId() {
        return STATE_ID;
    }

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, String input, Persistence persistence, Communication communication) {

        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(
            final Context context,
            final String input,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication) {
        return ImmutableStateDecision.builder()
                .nextStates(Arrays.asList(StateMovement.gracefulCompleteWorkflow("test-result")))
                .build();
    }
}
