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

package io.superdurable.dex.core.persistence;

import org.immutables.value.Value;

@Value.Immutable
public abstract class DataAttributeDef implements PersistenceFieldDef {
    public abstract Class getDataAttributeType();
    public abstract Boolean isPrefix();

    /**
     * Dex will verify if the key has been registered for the data attribute created using this method,
     * allowing users to create only one data attribute with the same key and data type.
     *
     * @param dataType  required.
     * @param key       required. The unique key.
     * @return a data attribute definition
     */
    public static DataAttributeDef create(final Class dataType, final String key) {
        return ImmutableDataAttributeDef.builder()
                .key(key)
                .dataAttributeType(dataType)
                .isPrefix(false)
                .build();
    }

    /**
     * Dex now supports dynamically created data attributes with a shared prefix and the same data type.
     * (E.g., dynamically created data attributes of type String can be named with a common prefix like: data_attribute_prefix_1: "one", data_attribute_prefix_2: "two")
     * Dex will verify if the prefix has been registered for data attributes created using this method,
     * allowing users to create multiple data attributes with the same prefix and data type.
     *
     * @param dataType      required.
     * @param keyPrefix     required. The common prefix of a set of keys to be created later.
     * @return a data attribute definition
     */
    public static DataAttributeDef createByPrefix(final Class dataType, final String keyPrefix) {
        return ImmutableDataAttributeDef.builder()
                .key(keyPrefix)
                .dataAttributeType(dataType)
                .isPrefix(true)
                .build();
    }
}
