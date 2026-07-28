/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
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
