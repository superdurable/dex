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
