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

package io.superdurable.dex.core;

import org.immutables.value.Value;

/**
 * A holder class for {@link WorkflowState} and it's metadata
 */
@Value.Immutable
public abstract class StateDef {

    public abstract WorkflowState getWorkflowState();

    // indicates if this state can be used to start a workflow
    public abstract boolean getCanStartWorkflow();

    public static StateDef startingState(WorkflowState state) {
        return ImmutableStateDef.builder()
                .canStartWorkflow(true)
                .workflowState(
                        state
                )
                .build();
    }

    public static StateDef nonStartingState(WorkflowState state) {
        return ImmutableStateDef.builder()
                .canStartWorkflow(false)
                .workflowState(
                        state
                )
                .build();
    }
}
