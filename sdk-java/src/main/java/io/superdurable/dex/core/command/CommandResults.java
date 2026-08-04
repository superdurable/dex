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
