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

import com.google.common.collect.ImmutableList;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDef;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class AbnormalExitWorkflow implements ObjectWorkflow {

    @Override
    public List<StateDef> getWorkflowStates() {
        return ImmutableList.of(StateDef.startingState(new AbnormalExitState1()));
    }
}
