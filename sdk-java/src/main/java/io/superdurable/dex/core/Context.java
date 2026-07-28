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

import java.util.Optional;

@Value.Immutable
public abstract class Context {
    public abstract Long getWorkflowStartTimestampSeconds();

    /**
     * @return the StateExecutionId.
     * Only applicable for state methods (waitUntil or execute)
     */
    public abstract Optional<String> getStateExecutionId();

    public abstract String getWorkflowRunId();

    public abstract String getWorkflowId();

    public abstract String getWorkflowType();

    /**
     * @return the start time of the first attempt of the state method invocation.
     * Only applicable for state methods (waitUntil or execute)
     */
    public abstract Optional<Long> getFirstAttemptTimestampSeconds();

    /**
     * @return attempt starts from 1, and increased by 1 for every retry if retry policy is specified.
     */
    public abstract Optional<Integer> getAttempt();

    /**
     * @return the requestId that is used to start the child workflow from state method.
     * Only applicable for state methods (waitUntil or execute)
     */
    public abstract Optional<String> getChildWorkflowRequestId();
}
