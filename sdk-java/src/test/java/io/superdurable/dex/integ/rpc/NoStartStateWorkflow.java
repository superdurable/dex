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
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.RPC;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.integ.RpcTest.RPC_OUTPUT;

@Component
public class NoStartStateWorkflow implements ObjectWorkflow {
    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.nonStartingState(new RpcWorkflowState2())
        );
    }

    @RPC
    public Long testRpcFunc1(Context context, String input, Persistence persistence, Communication communication) {
        if (context.getWorkflowId().isEmpty() || context.getWorkflowRunId().isEmpty()) {
            throw new RuntimeException("invalid context");
        }
        communication.triggerStateMovements(StateMovement.create(RpcWorkflowState2.class));
        return RPC_OUTPUT;
    }
}
