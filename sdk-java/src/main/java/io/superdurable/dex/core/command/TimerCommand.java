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

import org.immutables.value.Value;

import java.time.Duration;

@Value.Immutable
public abstract class TimerCommand implements BaseCommand {

    public abstract long getDurationSeconds();

    public static TimerCommand createByDuration(String commandId, Duration duration) {
        return ImmutableTimerCommand.builder()
                .commandId(commandId)
                .durationSeconds(duration.getSeconds())
                .build();
    }

    public static TimerCommand createByDuration(Duration duration) {
        return ImmutableTimerCommand.builder()
                .durationSeconds(duration.getSeconds())
                .build();
    }
}