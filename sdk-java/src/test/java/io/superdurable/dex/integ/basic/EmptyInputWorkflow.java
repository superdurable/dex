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
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class EmptyInputWorkflow implements ObjectWorkflow {

    public static final String CUSTOM_WF_TYPE = "test-customized-wf-type";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new EmptyInputWorkflowState1()),
                StateDef.nonStartingState(new EmptyInputWorkflowState2())
        );
    }

    @Override
    public String getWorkflowType() {
        return CUSTOM_WF_TYPE;
    }
}
