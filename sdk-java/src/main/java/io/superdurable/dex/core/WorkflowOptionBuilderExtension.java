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

import java.util.Arrays;

/**
 * This class is for extending {@link ImmutableWorkflowOptions.Builder} to provide a
 * better experience with strongly typing.
 */
public class WorkflowOptionBuilderExtension {
    private ImmutableWorkflowOptions.Builder builder = ImmutableWorkflowOptions.builder();

    /**
     * Add a state to wait for completion. This only waiting for all the completion of the state executions
     * NOTE: this will not be needed/required once server implements <a href="https://github.com/superdurable/dex/issues/349">this</a>
     * @param state The state to wait for completion.
     * @return The builder.
     */
    public WorkflowOptionBuilderExtension waitForCompletionState(Class<? extends WorkflowState> state) {
        this.waitForCompletionStates(state);
        return this;
    }

    /**
     * Add states to wait for completion. This only waiting for all the completion of the state executions
     * NOTE: this will not be needed/required once server implements <a href="https://github.com/superdurable/dex/issues/349">this</a>
     * @param states The states to wait for completion.
     * @return The builder.
     */
    @SafeVarargs
    public final WorkflowOptionBuilderExtension waitForCompletionStates(Class<? extends WorkflowState>... states) {
        Arrays.stream(states).forEach(
                state -> builder.addWaitForCompletionStateIds(
                        WorkflowState.getDefaultStateId(state)
                ));
        return this;
    }

    /**
     * Add a state to wait for completion. This can wait for a certain completion of the state execution
     * @param state The state to wait for completion.
     * @param number The number of the state completion to wait for. E.g. when it's 2, it's waiting for the second completion of the state.
     * @return The builder.
     */
    public WorkflowOptionBuilderExtension waitForCompletionStateWithNumber(Class<? extends WorkflowState> state, int number) {
        builder.addWaitForCompletionStateExecutionIds(
                WorkflowState.getStateExecutionId(state, number)
        );
        return this;
    }

    public ImmutableWorkflowOptions.Builder  getBuilder() {
        return builder;
    }
}
