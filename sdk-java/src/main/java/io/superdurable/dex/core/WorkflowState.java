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

import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;

import javax.annotation.Nullable;

import java.lang.reflect.Method;

public interface WorkflowState<I> {

    /**
     * This input type is needed for deserializing data back into Java object
     * @return the type of the state input
     */
    Class<I> getInputType();

    /**
     * Optionally implement this method to set up condition for the state.
     * If implemented, this will be the first API invoked when state started.
     * Then the state will be waiting until the requested commands are completed.
     * If not implemented, the state will invoke the {@link #execute} directly
     * <p>
     * The condition is setup using commands. There are three types commands in a {@link CommandRequest}: signal, timer and InternalChannel;
     * Also with three types of {@link io.superdurable.dex.gen.models.CommandWaitingType}
     *
     * @param context       the context info of this API invocation, like workflow start time, workflowId, etc
     * @param input         the state input which is deserialized by {@link ObjectEncoder} with {@link #getInputType}
     * @param persistence   persistence API for 1) data attributes, 2) search attributes and 3) stateExecutionLocals 4) recordEvent
     *                      DataAttributes and SearchAttributes are defined by {@link ObjectWorkflow} interface.
     *                      StateExecutionLocals are for passing data within the state execution from this waitUntil API to {@link #execute} API
     *                      RecordEvent is for storing some tracking info(e.g. RPC call input/output) when executing the API.
     *                      Note that any write API will be recorded to server after the whole waitUntil API response is accepted by server.
     * @param communication communication API, right now only for publishing value to InternalChannel
     *                      Note that any write API will be recorded to server after the whole waitUntil API response is accepted by server.
     * @return the requested commands for this step
     */
    default CommandRequest waitUntil(
            final Context context, I input,
            final Persistence persistence,
            final Communication communication) {
        /*
         * leaving this method with default implementation means the state doesn't have any condition for setup.
         * Dex will omit the waitUntil step and invoke the {@link #execute} API directly
         */
        throw new IllegalStateException("this exception will never be thrown.");
    }

    /**
     * Implement this method to execute the state business, when requested commands are ready if {@link #waitUntil} is implemented
     * If {@link #waitUntil} is not implemented, the state will invoke this execute API directly
     *
     * @param context        the context info of this API invocation, like workflow start time, workflowId, etc
     * @param input          the state input which is deserialized by {@link ObjectEncoder} with {@link #getInputType}
     * @param commandResults the results of the command that executed by {@link #waitUntil}
     * @param persistence    persistence API for 1) data attributes, 2) search attributes and 3) stateExecutionLocals 4) recordEvent
     *                       DataAttributes and SearchAttributes are defined by {@link ObjectWorkflow} interface.
     *                       StateExecutionLocals are for passing data within the state execution from this API to {@link #execute} API
     *                       RecordEvent is for storing some tracking info(e.g. RPC call input/output) when executing the API.
     *                       Note that the write API will be recorded to server after the whole execute API response is accepted by server.
     * @param communication  communication API, right now only for publishing value to InternalChannel
     *                       Note that the write API will be recorded to server after the whole execute API response is accepted by server.
     * @return the decision of what to do next(e.g. transition to next states)
     */
    StateDecision execute(
            final Context context,
            final I input,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication);

    /**
     * a unique identifier of the state
     * It must be unique in any workflow definition
     * By default just the simple name of the implementation class
     *
     * @return the StateId of the state
     */
    default String getStateId() {
        return getDefaultStateId(this.getClass());
    }

    /**
     * Optional configuration to adjust the state behaviors. Default values if not set:
     * - waitUntilApiFailurePolicy: FAIL_WORKFLOW_ON_FAILURE
     * - PersistenceLoadingPolicy for dataAttributes/searchAttributes: LOAD_ALL_WITHOUT_LOCKING ,
     * - waitUntil/execute API:
     * -    timeout: 30s
     * -    retryPolicy:
     * -        InitialIntervalSeconds: 1
     * -        MaxInternalSeconds:100
     * -        MaximumAttempts: 0
     * -        BackoffCoefficient: 2
     * @return the optional options
     */
    @Nullable
    default WorkflowStateOptions getStateOptions() {
        return null;
    }

    static boolean shouldSkipWaitUntil(final WorkflowState state) {
        final Class<? extends WorkflowState> stateClass = state.getClass();
        final Method waitUntilMethod;
        try {
            waitUntilMethod = stateClass.getMethod("waitUntil", Context.class, Object.class, Persistence.class, Communication.class);
        } catch (NoSuchMethodException e) {
            throw new IllegalStateException(e);
        }
        if (waitUntilMethod.getDeclaringClass().equals(WorkflowState.class)) {
            return true;
        }
        return false;
    }

    static String getStateExecutionId(Class<? extends WorkflowState> state, int number) {
        return String.format("%s-%d", state.getSimpleName(), number);
    }

    static String getDefaultStateId(Class<? extends WorkflowState> state) {
        return state.getSimpleName();
    }
}

