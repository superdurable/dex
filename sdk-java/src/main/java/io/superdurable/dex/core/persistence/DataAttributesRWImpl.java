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

import io.superdurable.dex.core.ObjectEncoder;
import io.superdurable.dex.core.TypeStore;
import io.superdurable.dex.gen.models.EncodedObject;
import io.superdurable.dex.gen.models.KeyValue;

import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

public class DataAttributesRWImpl implements DataAttributesRW {
    private final TypeStore typeStore;
    private final Map<String, EncodedObject> keyToEncodedObjectMap;
    private final Map<String, EncodedObject> toReturnToServer;
    private final ObjectEncoder objectEncoder;

    public DataAttributesRWImpl(
            final TypeStore typeStore,
            final Map<String, EncodedObject> keyToValueMap,
            final ObjectEncoder objectEncoder) {
        this.typeStore = typeStore;
        this.keyToEncodedObjectMap = keyToValueMap;
        this.toReturnToServer = new HashMap<>();
        this.objectEncoder = objectEncoder;
    }

    @Override
    public <T> T getDataAttribute(final String key, final Class<T> type) {
        if (!typeStore.isValidNameOrPrefix(key)) {
            throw new IllegalArgumentException(String.format("data attribute %s is not registered", key));
        }
        if (!keyToEncodedObjectMap.containsKey(key)) {
            return null;
        }

        final Class<?> registeredType = typeStore.getType(key);
        if (!type.isAssignableFrom(registeredType)) {
            throw new IllegalArgumentException(
                    String.format(
                            "registered type %s is not assignable from %s",
                            registeredType.getName(),
                            type.getName()));
        }

        return type.cast(
                objectEncoder.decode(keyToEncodedObjectMap.get(key), registeredType));
    }

    @Override
    public void setDataAttribute(final String key, final Object value) {
        if (!typeStore.isValidNameOrPrefix(key)) {
            throw new IllegalArgumentException(String.format("data attribute %s is not registered", key));
        }

        final Class<?> registeredType = typeStore.getType(key);
        if (value != null && !registeredType.isAssignableFrom(value.getClass())) {
            throw new IllegalArgumentException(String.format("Input is not an instance of class %s", registeredType.getName()));
        }

        this.keyToEncodedObjectMap.put(key, objectEncoder.encode(value));
        this.toReturnToServer.put(key, objectEncoder.encode(value));
    }

    public List<KeyValue> getToReturnToServer() {
        return toReturnToServer.entrySet().stream()
                .map(stringEncodedObjectEntry ->
                        new KeyValue()
                                .key(stringEncodedObjectEntry.getKey())
                                .value(stringEncodedObjectEntry.getValue()))
                .collect(Collectors.toList());
    }
}
