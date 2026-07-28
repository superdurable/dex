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

import io.superdurable.dex.gen.models.IDReusePolicy;
import io.superdurable.dex.gen.models.WorkflowAlreadyStartedOptions;
import io.superdurable.dex.gen.models.WorkflowConfig;
import io.superdurable.dex.gen.models.WorkflowRetryPolicy;
import org.immutables.value.Value;

import java.util.List;
import java.util.Map;
import java.util.Optional;

@Value.Immutable
public abstract class WorkflowOptions {
    public abstract Optional<IDReusePolicy> getWorkflowIdReusePolicy();

    public abstract Optional<String> getCronSchedule();

    public abstract Optional<Integer> getWorkflowStartDelaySeconds();

    public abstract Optional<WorkflowRetryPolicy> getWorkflowRetryPolicy();

    public abstract Map<String, Object> getInitialSearchAttribute();

    public abstract Map<String, Object> getInitialDataAttribute();

    public abstract Optional<WorkflowConfig> getWorkflowConfigOverride();

    public abstract List<String> getWaitForCompletionStateIds();
    public abstract List<String> getWaitForCompletionStateExecutionIds();

    public abstract Optional<WorkflowAlreadyStartedOptions> getWorkflowAlreadyStartedOptions();

    public static WorkflowOptionBuilderExtension extendedBuilder() {
        return new WorkflowOptionBuilderExtension();
    }

    public static ImmutableWorkflowOptions.Builder  basicBuilder() {
        return ImmutableWorkflowOptions.builder();
    }
}
