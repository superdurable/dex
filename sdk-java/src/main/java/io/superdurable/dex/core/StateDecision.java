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

import io.superdurable.dex.gen.models.WorkflowConditionalCloseType;
import org.immutables.value.Value;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Optional;

@Value.Immutable
public abstract class StateDecision {

    public abstract Optional<InternalConditionalClose> getWorkflowConditionalClose();

    public abstract List<StateMovement> getNextStates();

    // a dead end will just complete its thread, without triggering any closing workflow
    public static StateDecision deadEnd() {
        return ImmutableStateDecision.builder()
                .nextStates(Arrays.asList(StateMovement.DEAD_END_WORKFLOW_MOVEMENT))
                .build();
    }

    public static ImmutableStateDecision.Builder builder() {
        return ImmutableStateDecision.builder();
    }

    public static StateDecision gracefulCompleteWorkflow(final Object completionOutput) {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.gracefulCompleteWorkflow(completionOutput)
        )).build();
    }

    public static StateDecision gracefulCompleteWorkflow() {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.gracefulCompleteWorkflow()
        )).build();
    }

    public static StateDecision forceCompleteWorkflow(final Object completionOutput) {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.forceCompleteWorkflow(completionOutput)
        )).build();
    }

    public static StateDecision forceCompleteWorkflow() {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.forceCompleteWorkflow()
        )).build();
    }

    public static StateDecision forceFailWorkflow(final Object completionOutput) {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.forceFailWorkflow(completionOutput)
        )).build();
    }

    public static StateDecision forceFailWorkflow() {
        return ImmutableStateDecision.builder()
                .nextStates(Arrays.asList(StateMovement.FORCE_FAILING_WORKFLOW_MOVEMENT))
                .build();
    }

    public static <I> StateDecision forceCompleteIfInternalChannelEmptyOrElse(final String internalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass) {
        return forceCompleteIfInternalChannelEmptyOrElse(internalChannelName, orElseStateClass, null);
    }

    public static <I> StateDecision forceCompleteIfInternalChannelEmptyOrElse(final String internalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass, final I stateInput) {
        return forceCompleteIfInternalChannelEmptyOrElse(null, internalChannelName, StateMovement.create(orElseStateClass, stateInput));
    }

    public static <I> StateDecision forceCompleteIfInternalChannelEmptyOrElse(final Object completionOutput, final String internalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass) {
        return forceCompleteIfInternalChannelEmptyOrElse(completionOutput, internalChannelName, orElseStateClass, null);
    }

    public static <I> StateDecision forceCompleteIfInternalChannelEmptyOrElse(final Object completionOutput, final String internalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass, final I stateInput) {
        return forceCompleteIfInternalChannelEmptyOrElse(completionOutput, internalChannelName, StateMovement.create(orElseStateClass, stateInput));
    }

    /**
     * Atomically force complete the workflow if internal channel is empty, otherwise trigger the state movements from the current thread
     * This is important for use case that needs to ensure all the messages in the channel are processed before completing the workflow, otherwise messages will be lost.
     * Without this atomic API, if user just check the channel emptiness in the State APIs, the channel may receive new messages during the execution of state APIs
     *
     * @param completionOutput     the output of workflow completion
     * @param internalChannelName  the internal channel name for checking emptiness
     * @param orElseStateMovements the state movements if channel is not empty
     * @return the decision
     */
    public static StateDecision forceCompleteIfInternalChannelEmptyOrElse(final Object completionOutput, final String internalChannelName, final StateMovement... orElseStateMovements) {
        return ImmutableStateDecision.builder()
                .workflowConditionalClose(
                        ImmutableInternalConditionalClose.builder()
                                .workflowConditionalCloseType(WorkflowConditionalCloseType.FORCE_COMPLETE_ON_INTERNAL_CHANNEL_EMPTY)
                                .channelName(internalChannelName)
                                .closeInput(Optional.ofNullable(completionOutput))
                                .build()
                )
                .nextStates(Arrays.asList(orElseStateMovements))
                .build();
    }

    public static <I> StateDecision forceCompleteIfSignalChannelEmptyOrElse(final String signalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass) {
        return forceCompleteIfSignalChannelEmptyOrElse(signalChannelName, orElseStateClass, null);
    }

    public static <I> StateDecision forceCompleteIfSignalChannelEmptyOrElse(final String signalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass, final I stateInput) {
        return forceCompleteIfSignalChannelEmptyOrElse(null, signalChannelName, StateMovement.create(orElseStateClass, stateInput));
    }

    public static <I> StateDecision forceCompleteIfSignalChannelEmptyOrElse(final Object completionOutput, final String signalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass) {
        return forceCompleteIfSignalChannelEmptyOrElse(completionOutput, signalChannelName, orElseStateClass, null);
    }

    public static <I> StateDecision forceCompleteIfSignalChannelEmptyOrElse(final Object completionOutput, final String signalChannelName, final Class<? extends WorkflowState<I>> orElseStateClass, final I stateInput) {
        return forceCompleteIfSignalChannelEmptyOrElse(completionOutput, signalChannelName, StateMovement.create(orElseStateClass, stateInput));
    }

    /**
     * Atomically force complete the workflow if signal channel is empty, otherwise trigger the state movements from the current thread
     * This is important for use case that needs to ensure all the messages in the channel are processed before completing the workflow, otherwise messages will be lost.
     * Without this atomic API, if user just check the channel emptiness in the State APIs, the channel may receive new messages during the execution of state APIs
     *
     * @param completionOutput     the output of workflow completion
     * @param signalChannelName    the signal channel name for checking emptiness
     * @param orElseStateMovements the state movements if channel is not empty
     * @return the decision
     */
    public static StateDecision forceCompleteIfSignalChannelEmptyOrElse(final Object completionOutput, final String signalChannelName, final StateMovement... orElseStateMovements) {
        return ImmutableStateDecision.builder()
                .workflowConditionalClose(
                        ImmutableInternalConditionalClose.builder()
                                .workflowConditionalCloseType(WorkflowConditionalCloseType.FORCE_COMPLETE_ON_SIGNAL_CHANNEL_EMPTY)
                                .channelName(signalChannelName)
                                .closeInput(Optional.ofNullable(completionOutput))
                                .build()
                )
                .nextStates(Arrays.asList(orElseStateMovements))
                .build();
    }

    /**
     * @param <I>                  Class type of the WorkflowState input
     * @param stateClass           required
     * @param stateInput           optional, can be null
     * @param stateOptionsOverride optional, can be null. It is used to override the defined one in the State class
     * @return state decision
     */
    public static <I> StateDecision singleNextState(final Class<? extends WorkflowState<I>> stateClass, final I stateInput, final WorkflowStateOptions stateOptionsOverride) {
        return singleNextState(stateClass.getSimpleName(), stateInput, stateOptionsOverride);
    }

    /**
     * @param <I>        Class type of the WorkflowState input
     * @param stateClass required
     * @param stateInput optional, can be null
     * @return state decision
     */
    public static <I> StateDecision singleNextState(final Class<? extends WorkflowState<I>> stateClass, final I stateInput) {
        return singleNextState(stateClass.getSimpleName(), stateInput, null);
    }

    /**
     * @param <I>        Class type of the WorkflowState input
     * @param stateClass required
     * @param stateInput optional, can be null
     * @param waitForKey key awaited at state completion
     * @return state decision
     */
    public static <I> StateDecision singleNextState(final Class<? extends WorkflowState<I>> stateClass, final I stateInput, final String waitForKey) {
        return singleNextState(stateClass.getSimpleName(), stateInput, null, waitForKey);
    }

    /**
     * singleNextState as non-strongly typing required for input
     * @param stateClass required
     * @param stateInput optional, can be null
     * @return state decision
     */
    public static StateDecision singleNextStateUntypedInput(final Class<? extends WorkflowState> stateClass, final Object stateInput) {
        return singleNextState(stateClass.getSimpleName(), stateInput, null);
    }

    /**
     * @param <I>        Class type of the WorkflowState input
     * @param stateClass required
     * @return state decision
     */
    public static <I> StateDecision singleNextState(final Class<? extends WorkflowState<I>> stateClass) {
        return singleNextState(stateClass.getSimpleName(), null, null);
    }

    /**
     * use the other one with WorkflowState class param if the stateId is provided by default, to make your code cleaner
     *
     * @param stateId              required. StateId of next state
     * @param stateInput           optional, can be null. Input for next state
     * @param stateOptionsOverride optional, can be null. It is used to override the defined one in the State class
     * @return state decision
     */
    public static StateDecision singleNextState(final String stateId, final Object stateInput, final WorkflowStateOptions stateOptionsOverride) {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.create(stateId, stateInput, stateOptionsOverride)
        )).build();
    }

    /**
     * use the other one with WorkflowState class param if the stateId is provided by default, to make your code cleaner
     *
     * @param stateId              required. StateId of next state
     * @param stateInput           optional, can be null. Input for next state
     * @param stateOptionsOverride optional, can be null. It is used to override the defined one in the State class
     * @param waitForKey           key awaited at state completion
     * @return state decision
     */
    public static StateDecision singleNextState(final String stateId, final Object stateInput, final WorkflowStateOptions stateOptionsOverride, final String waitForKey) {
        return ImmutableStateDecision.builder().nextStates(Arrays.asList(
                StateMovement.create(stateId, stateInput, stateOptionsOverride, waitForKey)
        )).build();
    }

    /**
     * use the other one with WorkflowState class param if the stateId is provided by default, to make your code cleaner
     *
     * @param stateId stateId of next state
     * @return state decision
     */
    public static StateDecision singleNextState(final String stateId) {
        return singleNextState(stateId, null, null);
    }

    /**
     * @param stateMovements required
     * @return state decision
     */
    public static StateDecision multiNextStates(final List<StateMovement> stateMovements) {
        return ImmutableStateDecision.builder().nextStates(stateMovements).build();
    }

    /**
     * @param stateMovements required
     * @return state decision
     */
    public static StateDecision multiNextStates(final StateMovement... stateMovements) {
        return multiNextStates(Arrays.asList(stateMovements));
    }

    /**
     * use the other one with WorkflowState class param if the stateId is provided by default, to make your code cleaner
     * or use other ones with a list of StateMovement to enable the WorkflowStateOptions overriding
     *
     * @param stateIds stateIds of next states
     * @return state decision
     */
    public static StateDecision multiNextStates(final String... stateIds) {
        final ArrayList<StateMovement> stateMovements = new ArrayList<StateMovement>();
        Arrays.stream(stateIds).forEach(id -> {
            stateMovements.add(StateMovement.create(id));
        });
        return multiNextStates(stateMovements);
    }

    /**
     * use other ones with a list of StateMovement to enable the WorkflowStateOptions overriding
     *
     * @param states required
     * @return state decision
     */
    public static StateDecision multiNextStates(final Class<? extends WorkflowState>... states) {
        List<String> stateIds = new ArrayList<>();
        Arrays.stream(states).forEach(s -> stateIds.add(s.getSimpleName()));
        return multiNextStates(stateIds.toArray(new String[0]));
    }
}
