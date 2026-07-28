/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
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
