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

package io.superdurable.dex.core.mapper;

import io.superdurable.dex.core.ObjectEncoder;
import io.superdurable.dex.core.TypeStore;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.ImmutableCommandResults;

import java.util.stream.Collectors;

public class CommandResultsMapper {
    public static CommandResults fromGenerated(
            final io.superdurable.dex.gen.models.CommandResults commandResults,
            final TypeStore signalChannelTypeStore,
            final TypeStore internalChannelTypeStore,
            final ObjectEncoder objectEncoder) {

        ImmutableCommandResults.Builder builder = ImmutableCommandResults.builder();
        if (commandResults == null) {
            return builder.build();
        }
        if (commandResults.getSignalResults() != null) {
            builder.allSignalCommandResults(commandResults.getSignalResults().stream()
                    .map(signalResult -> SignalResultMapper.fromGenerated(
                            signalResult,
                            signalChannelTypeStore.getType(signalResult.getSignalChannelName()),
                            objectEncoder))
                    .collect(Collectors.toList()));
        }
        if (commandResults.getTimerResults() != null) {
            builder.allTimerCommandResults(commandResults.getTimerResults().stream()
                    .map(TimerResultMapper::fromGenerated)
                    .collect(Collectors.toList()));
        }
        if (commandResults.getInterStateChannelResults() != null) {
            builder.allInternalChannelCommandResult(commandResults.getInterStateChannelResults().stream()
                    .map(result -> InternalChannelResultMapper.fromGenerated(
                            result,
                            internalChannelTypeStore.getType(result.getChannelName()),
                            objectEncoder))
                    .collect(Collectors.toList()));
        }

        // The server will set stateWaitUntilFailed to true if the waitUntil API failed.
        // Hence, flag inversion is needed here to indicate that the waitUntil API
        // succeeded.
        builder.waitUntilApiSucceeded(true);
        if (Boolean.TRUE.equals(commandResults.getStateWaitUntilFailed())) {
            builder.waitUntilApiSucceeded(false);
        }
        return builder.build();
    }
}
