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
