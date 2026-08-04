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
