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

package io.superdurable.dex.core;

import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.persistence.DataAttributeDef;

import java.util.HashMap;
import java.util.Map;
import java.util.Optional;

/**
 * This class is used to store both the nameToTypeStore and prefixToTypeStore for the
 * data attribute, signal, and internal channel
 * in registry.
 */
public class TypeStore {
    public enum Type {
        DATA_ATTRIBUTE,
        SIGNAL_CHANNEL,
        INTERNAL_CHANNEL,
    }

    public final Type classType;
    public final Map<String, Class<?>> nameToTypeStore;
    public final Map<String, Class<?>> prefixToTypeStore;

    private TypeStore(final Type classType, final Map<String, Class<?>> nameToTypeStore, final Map<String, Class<?>> prefixToTypeStore) {
        this.classType = classType;
        this.nameToTypeStore = nameToTypeStore;
        this.prefixToTypeStore = prefixToTypeStore;
    }

    public static TypeStore defaultBuilder(final Type classType) {
        return new TypeStore(classType, new HashMap<>(), new HashMap<>());
    }

    public Boolean isValidNameOrPrefix(final String name) {
        final Class<?> type = doGetType(name);
        return type != null;
    }

    public Class<?> getType(final String name) {
        final Class<?> type = doGetType(name);

        if (type == null) {
            throw new IllegalArgumentException(
                    String.format(
                            "%s not registered: %s",
                            classType,
                            name
                    )
            );
        }

        return type;
    }

    public void addToStore(final Object def) {
        final boolean isPrefix;
        final String name;
        final Class<?> type;
        if (classType.equals(Type.DATA_ATTRIBUTE)) {
            final DataAttributeDef attributeDef = (DataAttributeDef) def;
            isPrefix = attributeDef.isPrefix();
            name = attributeDef.getKey();
            type = attributeDef.getDataAttributeType();
        } else {
            final CommunicationMethodDef channelDef = (CommunicationMethodDef) def;
            isPrefix = channelDef.isPrefix();
            name = channelDef.getName();
            type = channelDef.getValueType();
        }

        doAddToStore(isPrefix, name, type);
    }

    private Class<?> doGetType(final String name) {
        if (nameToTypeStore.containsKey(name)) {
            return nameToTypeStore.get(name);
        }

        final Optional<String> first = prefixToTypeStore.keySet().stream()
                .filter(name::startsWith)
                .findFirst();

        return first.map(prefixToTypeStore::get).orElse(null);
    }

    private void doAddToStore(final boolean isPrefix, final String name, final Class<?> type) {
        final Map<String, Class<?>> store;
        if (isPrefix) {
            store = prefixToTypeStore;
        } else {
            store = nameToTypeStore;
        }

        if (store.containsKey(name)) {
            throw new WorkflowDefinitionException(
                    String.format(
                            "%s name/prefix %s already exists",
                            classType,
                            name
                    )
            );
        }
        store.put(name, type);
    }
}
