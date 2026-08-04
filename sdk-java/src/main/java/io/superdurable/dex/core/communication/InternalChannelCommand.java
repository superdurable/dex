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

package io.superdurable.dex.core.communication;

import io.superdurable.dex.core.command.BaseCommand;
import org.immutables.value.Value;

@Value.Immutable
public abstract class InternalChannelCommand implements BaseCommand {

    public abstract String getChannelName();

    /**
     * Create one internal channel command.
     *
     * @param commandId     required.
     * @param channelName   required.
     * @return internal channel command
     */
    public static InternalChannelCommand create(final String commandId, final String channelName) {
        return ImmutableInternalChannelCommand.builder()
                .commandId(commandId)
                .channelName(channelName)
                .build();
    }

    /**
     * Create one internal channel command.
     *
     * @param channelName   required.
     * @return internal channel command
     */
    public static InternalChannelCommand create(final String channelName) {
        return ImmutableInternalChannelCommand.builder()
                .channelName(channelName)
                .build();
    }
}