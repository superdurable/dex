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
public abstract class SignalCommand implements BaseCommand {

    public abstract String getSignalChannelName();

    /**
     * Create one signal command.
     *
     * @param commandId    required.
     * @param signalName   required.
     * @return signal command
     */
    public static SignalCommand create(final String commandId, final String signalName) {
        return ImmutableSignalCommand.builder()
                .commandId(commandId)
                .signalChannelName(signalName)
                .build();
    }

    /**
     * Create one signal command.
     *
     * @param signalName     required.
     * @return signal command
     */
    public static SignalCommand create(final String signalName) {
        return ImmutableSignalCommand.builder()
                .signalChannelName(signalName)
                .build();
    }
}