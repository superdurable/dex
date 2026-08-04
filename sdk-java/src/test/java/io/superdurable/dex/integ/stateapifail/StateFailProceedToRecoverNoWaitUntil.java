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

package io.superdurable.dex.integ.stateapifail;

import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.gen.models.RetryPolicy;

public class StateFailProceedToRecoverNoWaitUntil extends StateFailBasic {
    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(StateRecoverNoWaitUntil.class)
                .setExecuteApiRetryPolicy(
                        new RetryPolicy()
                                .maximumAttempts(1)
                                .backoffCoefficient(2f)
                );
    }
}
