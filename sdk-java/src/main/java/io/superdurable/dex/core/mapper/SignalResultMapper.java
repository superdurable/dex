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
import io.superdurable.dex.core.communication.ImmutableSignalCommandResult;
import io.superdurable.dex.core.communication.SignalCommandResult;
import io.superdurable.dex.gen.models.SignalResult;

import java.util.Optional;

public class SignalResultMapper {
    public static SignalCommandResult fromGenerated(
            SignalResult signalResult,
            Class<?> signalType,
            ObjectEncoder objectEncoder) {
        return ImmutableSignalCommandResult.builder()
                .commandId(signalResult.getCommandId())
                .signalRequestStatusEnum(signalResult.getSignalRequestStatus())
                .signalChannelName(signalResult.getSignalChannelName())
                .signalValue(Optional.ofNullable(objectEncoder.decode(signalResult.getSignalValue(), signalType)))
                .build();
    }
}
