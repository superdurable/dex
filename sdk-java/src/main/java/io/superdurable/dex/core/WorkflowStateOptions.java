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

import io.superdurable.dex.gen.models.ExecuteApiFailurePolicy;
import io.superdurable.dex.gen.models.PersistenceLoadingPolicy;
import io.superdurable.dex.gen.models.RetryPolicy;

import java.util.ArrayList;
import java.util.Objects;

public class WorkflowStateOptions implements Cloneable {
    // Loading policy for search attributes, applies to both Wait Until and Execute API
    private PersistenceLoadingPolicy searchAttributesLoadingPolicy;

    // Loading policy for search attributes, applies only to Wait Until API
    private PersistenceLoadingPolicy waitUntilApiSearchAttributesLoadingPolicy;

    // Loading policy for search attributes, applies only to Execute API
    private PersistenceLoadingPolicy executeApiSearchAttributesLoadingPolicy;

    // Loading policy for data attributes, applies to both Wait Until and Execute API
    private PersistenceLoadingPolicy dataAttributesLoadingPolicy;

    // Loading policy for data attributes, applies only to Wait Until API
    private PersistenceLoadingPolicy waitUntilApiDataAttributesLoadingPolicy;

    // Loading policy for data attributes, applies only to Execute API
    private PersistenceLoadingPolicy executeApiDataAttributesLoadingPolicy;

    // Wait Until API specific options
    private Integer waitUntilApiTimeoutSeconds;

    private RetryPolicy waitUntilApiRetryPolicy;

    // Execute API specific options
    private Integer executeApiTimeoutSeconds;

    private RetryPolicy executeApiRetryPolicy;

    // To allow proceeding to the execute API after waitUntil API exhausted all retries.
    private Boolean proceedToExecuteWhenWaitUntilRetryExhausted;

    // The state to proceed to the specified state after the execute API exhausted all retries
    private Class<? extends WorkflowState> proceedToStateWhenExecuteRetryExhausted;

    // The state options override to use when proceeding to the configured state after the execute API exhausted all retries
    private WorkflowStateOptions proceedToStateWhenExecuteRetryExhaustedStateOptions;

    public PersistenceLoadingPolicy getSearchAttributesLoadingPolicy() {
        return searchAttributesLoadingPolicy;
    }

    public WorkflowStateOptions setSearchAttributesLoadingPolicy(PersistenceLoadingPolicy searchAttributesLoadingPolicy) {
        this.searchAttributesLoadingPolicy = searchAttributesLoadingPolicy;
        return this;
    }

    public PersistenceLoadingPolicy getWaitUntilApiSearchAttributesLoadingPolicy() {
        return waitUntilApiSearchAttributesLoadingPolicy;
    }

    public WorkflowStateOptions setWaitUntilApiSearchAttributesLoadingPolicy(PersistenceLoadingPolicy waitUntilApiSearchAttributesLoadingPolicy) {
        this.waitUntilApiSearchAttributesLoadingPolicy = waitUntilApiSearchAttributesLoadingPolicy;
        return this;
    }

    public PersistenceLoadingPolicy getExecuteApiSearchAttributesLoadingPolicy() {
        return executeApiSearchAttributesLoadingPolicy;
    }

    public WorkflowStateOptions setExecuteApiSearchAttributesLoadingPolicy(PersistenceLoadingPolicy executeApiSearchAttributesLoadingPolicy) {
        this.executeApiSearchAttributesLoadingPolicy = executeApiSearchAttributesLoadingPolicy;
        return this;
    }

    public PersistenceLoadingPolicy getDataAttributesLoadingPolicy() {
        return dataAttributesLoadingPolicy;
    }

    public WorkflowStateOptions setDataAttributesLoadingPolicy(PersistenceLoadingPolicy dataAttributesLoadingPolicy) {
        this.dataAttributesLoadingPolicy = dataAttributesLoadingPolicy;
        return this;
    }

    public PersistenceLoadingPolicy getWaitUntilApiDataAttributesLoadingPolicy() {
        return waitUntilApiDataAttributesLoadingPolicy;
    }

    public WorkflowStateOptions setWaitUntilApiDataAttributesLoadingPolicy(PersistenceLoadingPolicy waitUntilApiDataAttributesLoadingPolicy) {
        this.waitUntilApiDataAttributesLoadingPolicy = waitUntilApiDataAttributesLoadingPolicy;
        return this;
    }

    public PersistenceLoadingPolicy getExecuteApiDataAttributesLoadingPolicy() {
        return executeApiDataAttributesLoadingPolicy;
    }

    public WorkflowStateOptions setExecuteApiDataAttributesLoadingPolicy(PersistenceLoadingPolicy executeApiDataAttributesLoadingPolicy) {
        this.executeApiDataAttributesLoadingPolicy = executeApiDataAttributesLoadingPolicy;
        return this;
    }

    public Integer getWaitUntilApiTimeoutSeconds() {
        return waitUntilApiTimeoutSeconds;
    }

    public WorkflowStateOptions setWaitUntilApiTimeoutSeconds(Integer waitUntilApiTimeoutSeconds) {
        this.waitUntilApiTimeoutSeconds = waitUntilApiTimeoutSeconds;
        return this;
    }

    public Integer getExecuteApiTimeoutSeconds() {
        return executeApiTimeoutSeconds;
    }

    public WorkflowStateOptions setExecuteApiTimeoutSeconds(Integer executeApiTimeoutSeconds) {
        this.executeApiTimeoutSeconds = executeApiTimeoutSeconds;
        return this;
    }

    public RetryPolicy getWaitUntilApiRetryPolicy() {
        return waitUntilApiRetryPolicy;
    }

    public WorkflowStateOptions setWaitUntilApiRetryPolicy(RetryPolicy waitUntilApiRetryPolicy) {
        this.waitUntilApiRetryPolicy = waitUntilApiRetryPolicy;
        return this;
    }

    public RetryPolicy getExecuteApiRetryPolicy() {
        return executeApiRetryPolicy;
    }

    public WorkflowStateOptions setExecuteApiRetryPolicy(RetryPolicy executeApiRetryPolicy) {
        this.executeApiRetryPolicy = executeApiRetryPolicy;
        return this;
    }

    public Boolean getProceedToExecuteWhenWaitUntilRetryExhausted() {
        return proceedToExecuteWhenWaitUntilRetryExhausted;
    }

    /**
     * By default, workflow would fail after waitUntil API retry exhausted.
     * This policy is to allow proceeding to the execute API after waitUntil API exhausted all retries.
     * This is useful for some advanced use cases like SAGA pattern.
     * RetryPolicy is required to be set with maximumAttempts or maximumAttemptsDurationSeconds for waitUntil API.
     * <br>NOTE: execute API will use commandResults to check whether the waitUntil has succeeded or not.
     * <br>See more in <a href="https://github.com/superdurable/dex/wiki/WorkflowStateOptions">wiki</a>
     * @param proceed true to proceed to the execute API after waitUntil API exhausted all retries; false to fail.
     * @return this
     */
    public WorkflowStateOptions setProceedToExecuteWhenWaitUntilRetryExhausted(Boolean proceed) {
        this.proceedToExecuteWhenWaitUntilRetryExhausted = proceed;
        return this;
    }

    public Class<? extends WorkflowState> getProceedToStateWhenExecuteRetryExhausted() {
        return proceedToStateWhenExecuteRetryExhausted;
    }

    public WorkflowStateOptions getProceedToStateWhenExecuteRetryExhaustedStateOptions() {
        return proceedToStateWhenExecuteRetryExhaustedStateOptions;
    }

    /**
     * By default, workflow would fail after execute API retry exhausted.
     * Set the state to proceed to the specified state after the execute API exhausted all retries
     * This is useful for some advanced use cases like SAGA pattern.
     * RetryPolicy is required to be set with maximumAttempts or maximumAttemptsDurationSeconds for execute API.
     * <br>Note that the failure handling state will take the same input as the failed from state.
     * @param proceedToStateWhenExecuteRetryExhausted the state to proceed to after the execute API exhausted all retries
     * @return this
     */
    public WorkflowStateOptions setProceedToStateWhenExecuteRetryExhausted(Class<? extends WorkflowState> proceedToStateWhenExecuteRetryExhausted) {
        return setProceedToStateWhenExecuteRetryExhausted(proceedToStateWhenExecuteRetryExhausted, null);
    }

    /**
     * By default, workflow would fail after execute API retry exhausted.
     * Set the state to proceed to the specified state after the execute API exhausted all retries
     * This is useful for some advanced use cases like SAGA pattern.
     * RetryPolicy is required to be set with maximumAttempts or maximumAttemptsDurationSeconds for execute API.
     * <br>Note that the failure handling state will take the same input as the failed from state.
     * @param proceedToStateWhenExecuteRetryExhausted the state to proceed to after the execute API exhausted all retries
     * @param stateOptionsOverride the state options override to use when proceeding to the configured state after execute
     * API retry is exhausted
     * @return this
     */
    public WorkflowStateOptions setProceedToStateWhenExecuteRetryExhausted(
            Class<? extends WorkflowState> proceedToStateWhenExecuteRetryExhausted,
            WorkflowStateOptions stateOptionsOverride) {
        this.proceedToStateWhenExecuteRetryExhausted = proceedToStateWhenExecuteRetryExhausted;
        this.proceedToStateWhenExecuteRetryExhaustedStateOptions = stateOptionsOverride;
        return this;
    }

    @Override
    public boolean equals(Object o) {
        if (this == o) {
            return true;
        }
        if (o == null || getClass() != o.getClass()) {
            return false;
        }
        WorkflowStateOptions workflowStateOptions = (WorkflowStateOptions) o;
        return Objects.equals(this.searchAttributesLoadingPolicy, workflowStateOptions.searchAttributesLoadingPolicy)
                && Objects.equals(
                this.waitUntilApiSearchAttributesLoadingPolicy,
                workflowStateOptions.waitUntilApiSearchAttributesLoadingPolicy)
                && Objects.equals(
                this.executeApiSearchAttributesLoadingPolicy,
                workflowStateOptions.executeApiSearchAttributesLoadingPolicy)
                && Objects.equals(this.dataAttributesLoadingPolicy, workflowStateOptions.dataAttributesLoadingPolicy)
                && Objects.equals(
                this.waitUntilApiDataAttributesLoadingPolicy,
                workflowStateOptions.waitUntilApiDataAttributesLoadingPolicy)
                && Objects.equals(
                this.executeApiDataAttributesLoadingPolicy,
                workflowStateOptions.executeApiDataAttributesLoadingPolicy)
                && Objects.equals(this.waitUntilApiTimeoutSeconds, workflowStateOptions.waitUntilApiTimeoutSeconds)
                && Objects.equals(this.executeApiTimeoutSeconds, workflowStateOptions.executeApiTimeoutSeconds)
                && Objects.equals(this.waitUntilApiRetryPolicy, workflowStateOptions.waitUntilApiRetryPolicy)
                && Objects.equals(this.executeApiRetryPolicy, workflowStateOptions.executeApiRetryPolicy)
                && Objects.equals(
                this.proceedToExecuteWhenWaitUntilRetryExhausted,
                workflowStateOptions.proceedToExecuteWhenWaitUntilRetryExhausted)
                && Objects.equals(
                this.proceedToStateWhenExecuteRetryExhausted,
                workflowStateOptions.proceedToStateWhenExecuteRetryExhausted)
                && Objects.equals(
                this.proceedToStateWhenExecuteRetryExhaustedStateOptions,
                workflowStateOptions.proceedToStateWhenExecuteRetryExhaustedStateOptions);
    }

    @Override
    public int hashCode() {
        return Objects.hash(
                searchAttributesLoadingPolicy,
                waitUntilApiSearchAttributesLoadingPolicy,
                executeApiSearchAttributesLoadingPolicy,
                dataAttributesLoadingPolicy,
                waitUntilApiDataAttributesLoadingPolicy,
                executeApiDataAttributesLoadingPolicy,
                waitUntilApiTimeoutSeconds,
                executeApiTimeoutSeconds,
                waitUntilApiRetryPolicy,
                executeApiRetryPolicy,
                proceedToExecuteWhenWaitUntilRetryExhausted,
                proceedToStateWhenExecuteRetryExhausted,
                proceedToStateWhenExecuteRetryExhaustedStateOptions);
    }

    @Override
    public String toString() {
        StringBuilder sb = new StringBuilder();
        sb.append("class WorkflowStateOptions {\n");
        sb.append("    searchAttributesLoadingPolicy: ").append(toIndentedString(searchAttributesLoadingPolicy)).append("\n");
        sb.append("    waitUntilApiSearchAttributesLoadingPolicy: ")
                .append(toIndentedString(waitUntilApiSearchAttributesLoadingPolicy))
                .append("\n");
        sb.append("    executeApiSearchAttributesLoadingPolicy: ")
                .append(toIndentedString(executeApiSearchAttributesLoadingPolicy))
                .append("\n");
        sb.append("    dataAttributesLoadingPolicy: ").append(toIndentedString(dataAttributesLoadingPolicy)).append("\n");
        sb.append("    waitUntilApiDataAttributesLoadingPolicy: ")
                .append(toIndentedString(waitUntilApiDataAttributesLoadingPolicy))
                .append("\n");
        sb.append("    executeApiDataAttributesLoadingPolicy: ")
                .append(toIndentedString(executeApiDataAttributesLoadingPolicy))
                .append("\n");
        sb.append("    waitUntilApiTimeoutSeconds: ").append(toIndentedString(waitUntilApiTimeoutSeconds)).append("\n");
        sb.append("    executeApiTimeoutSeconds: ").append(toIndentedString(executeApiTimeoutSeconds)).append("\n");
        sb.append("    waitUntilApiRetryPolicy: ").append(toIndentedString(waitUntilApiRetryPolicy)).append("\n");
        sb.append("    executeApiRetryPolicy: ").append(toIndentedString(executeApiRetryPolicy)).append("\n");
        sb.append("    proceedToExecuteWhenWaitUntilRetryExhausted: ")
                .append(toIndentedString(proceedToExecuteWhenWaitUntilRetryExhausted))
                .append("\n");
        sb.append("    proceedToStateWhenExecuteRetryExhausted: ")
                .append(toIndentedString(proceedToStateWhenExecuteRetryExhausted))
                .append("\n");
        sb.append("    proceedToStateWhenExecuteRetryExhaustedStateOptions: ")
                .append(toIndentedString(proceedToStateWhenExecuteRetryExhaustedStateOptions))
                .append("\n");
        sb.append("}");
        return sb.toString();
    }

    /**
     * Convert the given object to string with each line indented by 4 spaces
     * (except the first line).
     */
    private String toIndentedString(Object o) {
        if (o == null) {
            return "null";
        }
        return o.toString().replace("\n", "\n    ");
    }

    @Override
    public WorkflowStateOptions clone() {
        final WorkflowStateOptions clone = new WorkflowStateOptions();

        clone.setSearchAttributesLoadingPolicy(clone(searchAttributesLoadingPolicy));
        clone.setWaitUntilApiSearchAttributesLoadingPolicy(clone(waitUntilApiSearchAttributesLoadingPolicy));
        clone.setExecuteApiDataAttributesLoadingPolicy(clone(executeApiSearchAttributesLoadingPolicy));
        clone.setDataAttributesLoadingPolicy(clone(dataAttributesLoadingPolicy));
        clone.setWaitUntilApiDataAttributesLoadingPolicy(clone(waitUntilApiDataAttributesLoadingPolicy));
        clone.setExecuteApiDataAttributesLoadingPolicy(clone(executeApiDataAttributesLoadingPolicy));
        clone.setWaitUntilApiTimeoutSeconds(waitUntilApiTimeoutSeconds);
        clone.setExecuteApiTimeoutSeconds(executeApiTimeoutSeconds);
        clone.setWaitUntilApiRetryPolicy(clone(waitUntilApiRetryPolicy));
        clone.setExecuteApiRetryPolicy(clone(executeApiRetryPolicy));
        clone.setProceedToExecuteWhenWaitUntilRetryExhausted(proceedToExecuteWhenWaitUntilRetryExhausted);
        clone.setProceedToStateWhenExecuteRetryExhausted(
                proceedToStateWhenExecuteRetryExhausted,
                proceedToStateWhenExecuteRetryExhaustedStateOptions);

        return clone;
    }

    // Perform a deep copy of PersistenceLoadingPolicy
    private PersistenceLoadingPolicy clone(PersistenceLoadingPolicy origPolicy) {
        if (origPolicy == null) {
            return null;
        }
        final PersistenceLoadingPolicy clone = new PersistenceLoadingPolicy();
        clone.setPersistenceLoadingType(origPolicy.getPersistenceLoadingType());
        clone.setPartialLoadingKeys(origPolicy.getPartialLoadingKeys() == null
                ? null
                : new ArrayList<>(origPolicy.getPartialLoadingKeys()));
        clone.setLockingKeys(origPolicy.getLockingKeys() == null ? null : new ArrayList<>(origPolicy.getLockingKeys()));
        clone.setUseKeyAsPrefix(origPolicy.getUseKeyAsPrefix());

        return clone;
    }

    // Perform a deep copy of RetryPolicy
    private RetryPolicy clone(RetryPolicy origPolicy) {
        if (origPolicy == null) {
            return null;
        }
        final RetryPolicy clone = new RetryPolicy();
        clone.setInitialIntervalSeconds(origPolicy.getInitialIntervalSeconds());
        clone.setBackoffCoefficient(origPolicy.getBackoffCoefficient());
        clone.setMaximumIntervalSeconds(origPolicy.getMaximumIntervalSeconds());
        clone.setMaximumAttempts(origPolicy.getMaximumAttempts());
        clone.setMaximumAttemptsDurationSeconds(origPolicy.getMaximumAttemptsDurationSeconds());

        return clone;
    }
}
