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

package io.superdurable.dex.core.mapper;

import io.superdurable.dex.core.InternalConditionalClose;
import io.superdurable.dex.core.ObjectEncoder;
import io.superdurable.dex.core.Registry;
import io.superdurable.dex.gen.models.EncodedObject;
import io.superdurable.dex.gen.models.StateDecision;
import io.superdurable.dex.gen.models.WorkflowConditionalClose;

import java.util.stream.Collectors;

public class StateDecisionMapper {
    public static StateDecision toGenerated(io.superdurable.dex.core.StateDecision stateDecision, final String workflowType, final Registry registry, final ObjectEncoder objectEncoder) {
        if (stateDecision.getNextStates() == null && !stateDecision.getWorkflowConditionalClose().isPresent()) {
            return null;
        }

        StateDecision decision = new StateDecision();

        if (stateDecision.getNextStates() != null) {
            decision.nextStates(
                    stateDecision.getNextStates()
                            .stream()
                            .map(movement -> StateMovementMapper.toGenerated(movement, workflowType, registry, objectEncoder))
                            .collect(Collectors.toList())
            );
        }

        if (!stateDecision.getWorkflowConditionalClose().isPresent()) {
            return decision;
        }

        InternalConditionalClose conditionalClose = stateDecision.getWorkflowConditionalClose().get();
        EncodedObject closeInput = objectEncoder.encode(conditionalClose.getCloseInput());
        decision.conditionalClose(
                new WorkflowConditionalClose()
                        .conditionalCloseType(conditionalClose.getWorkflowConditionalCloseType())
                        .closeInput(closeInput)
                        .channelName(conditionalClose.getChannelName())
        );
        return decision;
    }
}
