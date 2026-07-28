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

package io.superdurable.dex.core.command;

import io.superdurable.dex.core.communication.InternalChannelCommandResult;
import io.superdurable.dex.core.communication.SignalCommandResult;
import org.immutables.value.Value;

import java.util.List;
import java.util.Optional;

/**
 * This is the container of all requested commands' results/statuses
 */
@Value.Immutable
public abstract class CommandResults {

    //public abstract List<LongRunningActivityCommandResult> getAllLongRunningActivityCommandResults();
    public abstract List<SignalCommandResult> getAllSignalCommandResults();

    public abstract List<TimerCommandResult> getAllTimerCommandResults();

    public abstract List<InternalChannelCommandResult> getAllInternalChannelCommandResult();

    public abstract Optional<Boolean> getWaitUntilApiSucceeded();

    // below are helpers
    public <T> T getSignalValueByIndex(int idx) {
        final List<SignalCommandResult> results = getAllSignalCommandResults();
        final SignalCommandResult value = results.get(idx);
        return (T) value.getSignalValue().get();
    }

    public <T> T getSignalValueById(String commandId) {
        final List<SignalCommandResult> results = getAllSignalCommandResults();
        for (SignalCommandResult result : results) {
            if (result.getCommandId().equals(commandId)) {
                return (T) result.getSignalValue().get();
            }
        }
        throw new IllegalArgumentException("commandId not found");
    }
}
