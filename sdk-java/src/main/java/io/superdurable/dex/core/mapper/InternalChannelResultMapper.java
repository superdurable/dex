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
import io.superdurable.dex.core.communication.ImmutableInternalChannelCommandResult;
import io.superdurable.dex.core.communication.InternalChannelCommandResult;
import io.superdurable.dex.gen.models.InterStateChannelResult;

import java.util.Optional;

public class InternalChannelResultMapper {
    public static InternalChannelCommandResult fromGenerated(
            InterStateChannelResult result,
            Class<?> type,
            ObjectEncoder objectEncoder) {
        return ImmutableInternalChannelCommandResult.builder()
                .commandId(result.getCommandId())
                .requestStatusEnum(result.getRequestStatus())
                .channelName(result.getChannelName())
                .value(Optional.ofNullable(objectEncoder.decode(result.getValue(), type)))
                .build();
    }
}
