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

import org.immutables.value.Value;

@Value.Immutable
public abstract class SignalChannelDef implements CommunicationMethodDef {

    /**
     * Dex will verify if the name has been registered for the signal channel created using this method,
     * allowing users to create only one signal channel with the same name and type.
     *
     * @param type  required.
     * @param name  required. The unique name.
     * @return a signal channel definition
     */
    public static SignalChannelDef create(final Class type, final String name) {
        return ImmutableSignalChannelDef.builder()
                .name(name)
                .valueType(type)
                .isPrefix(false)
                .build();
    }

    /**
     * Dex now supports dynamically created signal channels with a shared name prefix and the same type.
     * (E.g., dynamically created signal channels of type String can be named with a common prefix like: signal_channel_prefix_1: "one", signal_channel_prefix_2: "two")
     * Dex will verify if the prefix has been registered for signal channels created using this method,
     * allowing users to create multiple signal channels with the same name prefix and type.
     *
     * @param type          required.
     * @param namePrefix    required. The common name prefix of a set of signal channels to be created later.
     * @return a signal channel definition
     */
    public static SignalChannelDef createByPrefix(final Class type, final String namePrefix) {
        return ImmutableSignalChannelDef.builder()
                .name(namePrefix)
                .valueType(type)
                .isPrefix(true)
                .build();
    }
}
