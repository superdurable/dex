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
