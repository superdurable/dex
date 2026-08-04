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

import io.superdurable.dex.gen.models.IDReusePolicy;
import io.superdurable.dex.gen.models.SearchAttribute;
import io.superdurable.dex.gen.models.WorkflowAlreadyStartedOptions;
import io.superdurable.dex.gen.models.WorkflowConfig;
import io.superdurable.dex.gen.models.WorkflowRetryPolicy;
import io.superdurable.dex.gen.models.WorkflowStateOptions;
import org.immutables.value.Value;

import java.util.List;
import java.util.Map;
import java.util.Optional;

@Value.Immutable
public abstract class UnregisteredWorkflowOptions {
    public abstract Optional<IDReusePolicy> getWorkflowIdReusePolicy();

    public abstract Optional<String> getCronSchedule();

    public abstract Optional<Integer> getWorkflowStartDelaySeconds();

    public abstract Optional<WorkflowRetryPolicy> getWorkflowRetryPolicy();

    public abstract Optional<WorkflowStateOptions> getStartStateOptions();

    public abstract List<SearchAttribute> getInitialSearchAttribute();

    public abstract Map<String, Object> getInitialDataAttribute();

    public abstract Optional<WorkflowConfig> getWorkflowConfigOverride();

    public abstract Optional<Boolean> getUsingMemoForDataAttributes();

    public abstract List<String> getWaitForCompletionStateExecutionIds();

    public abstract List<String> getWaitForCompletionStateIds();

    public abstract Optional<WorkflowAlreadyStartedOptions> getWorkflowAlreadyStartedOptions();
}
