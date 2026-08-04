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

import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.gen.models.InterStateChannelCommand;

public class InterStateChannelCommandMapper {
    public static InterStateChannelCommand toGenerated(InternalChannelCommand stateChannelCommand) {
        final InterStateChannelCommand command = new InterStateChannelCommand()
                .channelName(stateChannelCommand.getChannelName());
        if (stateChannelCommand.getCommandId().isPresent()) {
            command.commandId(stateChannelCommand.getCommandId().get());
        }
        return command;
    }
}
