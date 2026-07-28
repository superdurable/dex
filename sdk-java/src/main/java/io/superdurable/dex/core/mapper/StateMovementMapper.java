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

package io.superdurable.dex.core.mapper;

import io.superdurable.dex.core.ObjectEncoder;
import io.superdurable.dex.core.Registry;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowDefinitionException;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.gen.models.ExecuteApiFailurePolicy;
import io.superdurable.dex.gen.models.RetryPolicy;
import io.superdurable.dex.gen.models.StateMovement;
import io.superdurable.dex.gen.models.WaitUntilApiFailurePolicy;
import io.superdurable.dex.gen.models.WorkflowStateOptions;

import static io.superdurable.dex.core.StateMovement.RESERVED_STATE_ID_PREFIX;
import static io.superdurable.dex.core.WorkflowState.shouldSkipWaitUntil;

public class StateMovementMapper {

    public static StateMovement toGenerated(final io.superdurable.dex.core.StateMovement stateMovement, final String workflowType, final Registry registry, final ObjectEncoder objectEncoder) {
        final Object input = stateMovement.getStateInput().orElse(null);
        final StateMovement movement = new StateMovement()
                .stateId(stateMovement.getStateId())
                .stateInput(objectEncoder.encode(input));
        if (!stateMovement.getStateId().startsWith(RESERVED_STATE_ID_PREFIX)) {
            final StateDef stateDef = registry.getWorkflowState(workflowType, stateMovement.getStateId());
            if(stateDef == null){
                throw new IllegalArgumentException("state "+stateMovement.getStateId() +" is not registered in the workflow "+workflowType);
            }

            // Try to get the overrode stateOptions, if it's null, get the stateOptions from stateDef
            WorkflowStateOptions stateOptions;
            if (stateMovement.getStateOptionsOverride().isPresent()) {
                stateOptions =
                        toIdlWorkflowStateOptionsWithValidation(stateMovement.getStateOptionsOverride().get(), stateMovement.getStateId());
            } else {
                stateOptions = toIdlWorkflowStateOptionsWithValidation(stateDef);
            }

            if (shouldSkipWaitUntil(stateDef.getWorkflowState())) {
                if (stateOptions == null) {
                    stateOptions = new WorkflowStateOptions();
                }

                stateOptions.skipWaitUntil(true);
            }

            autoFillFailureProceedingStateOptions(stateOptions, workflowType, registry);

            if (stateOptions != null) {
                movement.stateOptions(stateOptions);
            }

            stateMovement.getWaitForKey().ifPresent(movement::waitForKey);
        }
        return movement;
    }

    public static void autoFillFailureProceedingStateOptions(WorkflowStateOptions stateOptions, final String workflowType, final Registry registry) {
        if (stateOptions == null) {
            return;
        }
        if (stateOptions.getExecuteApiFailurePolicy() == ExecuteApiFailurePolicy.PROCEED_TO_CONFIGURED_STATE
                && stateOptions.getExecuteApiFailureProceedStateOptions() == null) {

            // fill the state options for the proceeding state
            String proceedStateId = stateOptions.getExecuteApiFailureProceedStateId();
            final StateDef proceedStatDef = registry.getWorkflowState(workflowType, proceedStateId);
            WorkflowStateOptions proceedStateOptions = toIdlWorkflowStateOptionsWithValidation(proceedStatDef);
            if (proceedStateOptions != null &&
                    proceedStateOptions.getExecuteApiFailurePolicy() == ExecuteApiFailurePolicy.PROCEED_TO_CONFIGURED_STATE) {
                throw new WorkflowDefinitionException("nested failure handling is not supported. You cannot set a failure proceeding state on top of another failure proceeding state.");
            }

            if (shouldSkipWaitUntil(proceedStatDef.getWorkflowState())) {
                if (proceedStateOptions == null) {
                    proceedStateOptions = new WorkflowStateOptions().skipWaitUntil(true);
                } else {
                    proceedStateOptions.skipWaitUntil(true);
                }
            }

            stateOptions.executeApiFailureProceedStateOptions(proceedStateOptions);
        }
    }

    /**
     * Validates if the required fields are present when the {@link WorkflowStateOptions} is configured to proceed on Execute
     * API Failure.
     * @param stateOptions the state options to validate.
     * @param stateId the unique ID of the state
     */
    private static void validateExecuteApiFailurePolicy(WorkflowStateOptions stateOptions, String stateId) {
        // Validate required fields if Execute failure policy is configured to proceed
        if (stateOptions.getExecuteApiFailurePolicy() == ExecuteApiFailurePolicy.PROCEED_TO_CONFIGURED_STATE) {
            // retry policy must be set
            if (stateOptions.getExecuteApiRetryPolicy() == null) {
                throw new WorkflowDefinitionException("RetryPolicy must be set for the execute " + stateId);
            }
            final RetryPolicy policy = stateOptions.getExecuteApiRetryPolicy();
            // either maximumAttempts or maximumAttemptsDurationSeconds must be set and greater than zero
            if (policy.getMaximumAttempts() == null && policy.getMaximumAttemptsDurationSeconds() == null) {
                throw new WorkflowDefinitionException(
                        "Either maximumAttempts or maximumAttemptsDurationSeconds must be set for the execute " + stateId);
            }
        }
    }

    /**
     * Validates if the required fields are present when the {@link WorkflowStateOptions} is configured to proceed on WaitUntil
     * API Failure.
     * @param stateOptions the state options to validate.
     * @param stateId the unique ID of the state
     */
    private static void validateWaitUntilApiFailurePolicy(WorkflowStateOptions stateOptions, String stateId) {
        // Validate required fields if Wait Until failure policy is configured to proceed
        if (stateOptions.getWaitUntilApiFailurePolicy() == WaitUntilApiFailurePolicy.PROCEED_ON_FAILURE) {
            // retry policy must be set
            if (stateOptions.getWaitUntilApiRetryPolicy() == null) {
                throw new WorkflowDefinitionException("RetryPolicy must be set for the waitUntil " + stateId);
            }
            final RetryPolicy policy = stateOptions.getWaitUntilApiRetryPolicy();
            // either maximumAttempts or maximumAttemptsDurationSeconds must be set and greater than zero
            if (policy.getMaximumAttempts() == null && policy.getMaximumAttemptsDurationSeconds() == null) {
                throw new WorkflowDefinitionException(
                        "Either maximumAttempts or maximumAttemptsDurationSeconds must be set for the waitUntil " + stateId);
            }
        }
    }

    public static WorkflowStateOptions toIdlWorkflowStateOptionsWithValidation(final StateDef stateDef) {
        final WorkflowState state = stateDef.getWorkflowState();
        if (state.getStateOptions() == null) {
            return null;
        }

        return toIdlWorkflowStateOptionsWithValidation(state.getStateOptions(), state.getStateId());
    }

    public static WorkflowStateOptions toIdlWorkflowStateOptionsWithValidation(
            io.superdurable.dex.core.WorkflowStateOptions stateOptions,
            String stateId) {
        if (stateOptions == null) {
            return null;
        }

        // Guarantee workflow state options copy is not holding references to the original by cloning object
        stateOptions = stateOptions.clone();

        final WorkflowStateOptions idlWorkflowStateOptions = new WorkflowStateOptions();

        idlWorkflowStateOptions.setSearchAttributesLoadingPolicy(stateOptions.getSearchAttributesLoadingPolicy());
        idlWorkflowStateOptions.setWaitUntilApiSearchAttributesLoadingPolicy(stateOptions.getWaitUntilApiSearchAttributesLoadingPolicy());
        idlWorkflowStateOptions.setExecuteApiSearchAttributesLoadingPolicy(stateOptions.getExecuteApiSearchAttributesLoadingPolicy());
        idlWorkflowStateOptions.setDataAttributesLoadingPolicy(stateOptions.getDataAttributesLoadingPolicy());
        idlWorkflowStateOptions.setWaitUntilApiDataAttributesLoadingPolicy(stateOptions.getWaitUntilApiDataAttributesLoadingPolicy());
        idlWorkflowStateOptions.setExecuteApiDataAttributesLoadingPolicy(stateOptions.getExecuteApiDataAttributesLoadingPolicy());
        idlWorkflowStateOptions.setWaitUntilApiTimeoutSeconds(stateOptions.getWaitUntilApiTimeoutSeconds());
        idlWorkflowStateOptions.setExecuteApiTimeoutSeconds(stateOptions.getExecuteApiTimeoutSeconds());
        idlWorkflowStateOptions.setWaitUntilApiRetryPolicy(stateOptions.getWaitUntilApiRetryPolicy());
        idlWorkflowStateOptions.setExecuteApiRetryPolicy(stateOptions.getExecuteApiRetryPolicy());
        if (stateOptions.getProceedToExecuteWhenWaitUntilRetryExhausted() != null) {
            idlWorkflowStateOptions.setWaitUntilApiFailurePolicy(Boolean.TRUE.equals(stateOptions.getProceedToExecuteWhenWaitUntilRetryExhausted())
                    ? WaitUntilApiFailurePolicy.PROCEED_ON_FAILURE
                    : WaitUntilApiFailurePolicy.FAIL_WORKFLOW_ON_FAILURE);
        }
        if (stateOptions.getProceedToStateWhenExecuteRetryExhausted() != null) {
            idlWorkflowStateOptions.setExecuteApiFailurePolicy(ExecuteApiFailurePolicy.PROCEED_TO_CONFIGURED_STATE);
            idlWorkflowStateOptions.setExecuteApiFailureProceedStateId(stateOptions.getProceedToStateWhenExecuteRetryExhausted()
                    .getSimpleName());
        }
        if (stateOptions.getProceedToStateWhenExecuteRetryExhaustedStateOptions() != null) {
            idlWorkflowStateOptions.setExecuteApiFailureProceedStateOptions(toIdlWorkflowStateOptionsWithValidation(
                    stateOptions.getProceedToStateWhenExecuteRetryExhaustedStateOptions(),
                    stateId));
        }

        validateExecuteApiFailurePolicy(idlWorkflowStateOptions, stateId);
        validateWaitUntilApiFailurePolicy(idlWorkflowStateOptions, stateId);

        return idlWorkflowStateOptions;
    }
}
