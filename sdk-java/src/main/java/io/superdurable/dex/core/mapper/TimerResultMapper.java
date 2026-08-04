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

import io.superdurable.dex.core.command.ImmutableTimerCommandResult;
import io.superdurable.dex.core.command.TimerCommandResult;
import io.superdurable.dex.gen.models.TimerResult;

public class TimerResultMapper {
    public static TimerCommandResult fromGenerated(
            TimerResult timerResult) {
        return ImmutableTimerCommandResult.builder()
                .commandId(timerResult.getCommandId())
                .timerStatus(timerResult.getTimerStatus())
                .build();
    }
}
