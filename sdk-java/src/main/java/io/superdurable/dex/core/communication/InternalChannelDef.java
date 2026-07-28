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
public abstract class InternalChannelDef implements CommunicationMethodDef {

    /**
     * Dex will verify if the name has been registered for the internal channel created using this method,
     * allowing users to create only one internal channel with the same name and type.
     *
     * @param type  required.
     * @param name  required. The unique name.
     * @return an internal channel definition
     */
    public static InternalChannelDef create(final Class type, final String name) {
        return ImmutableInternalChannelDef.builder()
                .name(name)
                .valueType(type)
                .isPrefix(false)
                .build();
    }

    /**
     * Dex now supports dynamically created internal channels with a shared name prefix and the same type.
     * (E.g., dynamically created internal channels of type String can be named with a common prefix like: internal_channel_prefix_1: "one", internal_channel_prefix_2: "two")
     * Dex will verify if the prefix has been registered for internal channels created using this method,
     * allowing users to create multiple internal channels with the same name prefix and type.
     *
     * @param type          required.
     * @param namePrefix    required. The common name prefix of a set of internal channels to be created later.
     * @return an internal channel definition
     */
    public static InternalChannelDef createByPrefix(final Class type, final String namePrefix) {
        return ImmutableInternalChannelDef.builder()
                .name(namePrefix)
                .valueType(type)
                .isPrefix(true)
                .build();
    }
}
