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

import io.superdurable.dex.gen.models.WorkflowResetType;
import org.immutables.value.Value;

import java.util.Optional;

@Value.Immutable
public abstract class ResetWorkflowTypeAndOptions {

    public abstract WorkflowResetType getResetType();

    public abstract Optional<Integer> getHistoryEventId();

    public abstract String getReason();

    public abstract Optional<String> getHistoryEventTime();

    public abstract Optional<String> getStateId();

    public abstract Optional<String> getStateExecutionId();

    public abstract Optional<Boolean> getSkipSignalReapply();

    public abstract Optional<Boolean> getSkipUpdateReapply();

    public static ResetWorkflowTypeAndOptions resetToBeginning(final String reason) {
        return builder()
                .resetType(WorkflowResetType.BEGINNING)
                .reason(reason)
                .build();
    }

    public static ResetWorkflowTypeAndOptions resetToHistoryEventId(final int historyEventId, final String reason) {
        return builder()
                .resetType(WorkflowResetType.HISTORY_EVENT_ID)
                .historyEventId(historyEventId)
                .reason(reason)
                .build();
    }

    public static ResetWorkflowTypeAndOptions resetToHistoryEventId(final String historyEventTime, final String reason) {
        return builder()
                .resetType(WorkflowResetType.HISTORY_EVENT_ID)
                .historyEventTime(historyEventTime)
                .reason(reason)
                .build();
    }

    public static ResetWorkflowTypeAndOptions resetToStateId(final String stateId, final String reason) {
        return builder()
                .resetType(WorkflowResetType.STATE_ID)
                .stateId(stateId)
                .reason(reason)
                .build();
    }

    public static ResetWorkflowTypeAndOptions resetToStateExecutionId(final String stateExecution, final String reason) {
        return builder()
                .resetType(WorkflowResetType.STATE_EXECUTION_ID)
                .stateExecutionId(stateExecution)
                .reason(reason)
                .build();
    }

    public static ImmutableResetWorkflowTypeAndOptions.Builder builder() {
        return ImmutableResetWorkflowTypeAndOptions.builder();
    }
}
