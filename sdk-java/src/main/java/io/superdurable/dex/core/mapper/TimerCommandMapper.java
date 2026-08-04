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

import io.superdurable.dex.gen.models.TimerCommand;

public class TimerCommandMapper {
    public static TimerCommand toGenerated(io.superdurable.dex.core.command.TimerCommand timerCommand) {
        final TimerCommand command = new TimerCommand()
                .durationSeconds(timerCommand.getDurationSeconds());
        if (timerCommand.getCommandId().isPresent()) {
            command.commandId(timerCommand.getCommandId().get());
        }
        return command;
    }
}
