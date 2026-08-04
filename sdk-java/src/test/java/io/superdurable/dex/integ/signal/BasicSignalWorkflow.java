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

package io.superdurable.dex.integ.signal;

import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.SignalChannelDef;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class BasicSignalWorkflow implements ObjectWorkflow {

    public static final String SIGNAL_CHANNEL_NAME_1 = "test-signal-1";

    public static final String SIGNAL_CHANNEL_NAME_2 = "test-signal-2";

    public static final String SIGNAL_CHANNEL_NAME_3 = "test-signal-3";
    public static final String SIGNAL_CHANNEL_PREFIX_1 = "test-signal-prefix-1";

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Integer.class, SIGNAL_CHANNEL_NAME_1),
                SignalChannelDef.create(Integer.class, SIGNAL_CHANNEL_NAME_2),
                SignalChannelDef.create(Void.class, SIGNAL_CHANNEL_NAME_3),
                SignalChannelDef.createByPrefix(Integer.class, SIGNAL_CHANNEL_PREFIX_1)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new BasicSignalWorkflowState1()),
                StateDef.nonStartingState(new BasicSignalWorkflowState2())
        );
    }
}
