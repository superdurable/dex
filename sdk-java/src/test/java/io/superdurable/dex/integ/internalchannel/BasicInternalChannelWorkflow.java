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

package io.superdurable.dex.integ.internalchannel;

import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.InternalChannelDef;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class BasicInternalChannelWorkflow implements ObjectWorkflow {
    public static final String INTER_STATE_CHANNEL_NAME_1 = "test-inter-state-channel-1";

    public static final String INTER_STATE_CHANNEL_NAME_2 = "test-inter-state-channel-2";
    public static final String INTER_STATE_CHANNEL_PREFIX_1 = "test-inter-state-channel-prefix-1-";

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(Integer.class, INTER_STATE_CHANNEL_NAME_1),
                InternalChannelDef.create(Integer.class, INTER_STATE_CHANNEL_NAME_2),
                InternalChannelDef.createByPrefix(Integer.class, INTER_STATE_CHANNEL_PREFIX_1)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new BasicInternalChannelWorkflowState0()),
                StateDef.nonStartingState(new BasicInternalChannelWorkflowState1()),
                StateDef.nonStartingState(new BasicInternalChannelWorkflowState2())
        );
    }
}
