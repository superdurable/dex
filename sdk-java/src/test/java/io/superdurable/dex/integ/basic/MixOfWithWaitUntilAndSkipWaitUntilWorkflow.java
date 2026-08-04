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

import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.gen.models.RetryPolicy;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class MixOfWithWaitUntilAndSkipWaitUntilWorkflow implements ObjectWorkflow {

    public static WorkflowStateOptions SHARED_STATE_OPTIONS =
            new WorkflowStateOptions().setExecuteApiRetryPolicy(new RetryPolicy().maximumAttempts(3));

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new MixOfWithWaitUntilAndSkipWaitUntilState1()),
                StateDef.nonStartingState(new MixOfWithWaitUntilAndSkipWaitUntilState2())
        );
    }
}
