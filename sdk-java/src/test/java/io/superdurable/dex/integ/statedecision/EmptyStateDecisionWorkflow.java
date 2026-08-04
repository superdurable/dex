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

package io.superdurable.dex.integ.statedecision;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.Collections;
import java.util.List;

@Component
public class EmptyStateDecisionWorkflow implements ObjectWorkflow {
    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new EmptyStateDecisionState1())
        );
    }
}

class EmptyStateDecisionState1 implements WorkflowState<Integer> {
    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer useSignal,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication
    ) {
        List<StateMovement> emptyList = Collections.emptyList();
        return StateDecision.multiNextStates(emptyList);
    }
}