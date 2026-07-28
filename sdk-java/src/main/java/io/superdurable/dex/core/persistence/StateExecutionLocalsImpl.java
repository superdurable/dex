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
import io.superdurable.dex.core.WorkflowDefinitionException;
import io.superdurable.dex.gen.models.EncodedObject;
import io.superdurable.dex.gen.models.KeyValue;

import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

public class StateExecutionLocalsImpl implements StateExecutionLocals {

    private final Map<String, EncodedObject> recordEvents;
    private final Map<String, EncodedObject> attributeNameToEncodedObjectMap;
    private final Map<String, EncodedObject> upsertAttributesToReturnToServer;
    private final ObjectEncoder objectEncoder;

    public StateExecutionLocalsImpl(final Map<String, EncodedObject> attributeNameToEncodedObjectMap,
                                    final ObjectEncoder objectEncoder) {
        this.objectEncoder = objectEncoder;
        this.attributeNameToEncodedObjectMap = attributeNameToEncodedObjectMap;
        upsertAttributesToReturnToServer = new HashMap<>();
        recordEvents = new HashMap<>();
    }

    @Override
    public void setStateExecutionLocal(final String key, final Object value) {
        final EncodedObject encodedData = objectEncoder.encode(value);
        attributeNameToEncodedObjectMap.put(key, encodedData);
        upsertAttributesToReturnToServer.put(key, encodedData);
    }

    @Override
    public <T> T getStateExecutionLocal(final String key, final Class<T> type) {
        final EncodedObject encodedData = this.attributeNameToEncodedObjectMap.get(key);
        if (encodedData == null) {
            return null;
        }
        return objectEncoder.decode(encodedData, type);
    }

    @Override
    public void recordEvent(final String key, final Object... eventData) {
        if (recordEvents.containsKey(key)) {
            throw new WorkflowDefinitionException("cannot record the same event for more than once");
        }
        if (eventData != null && eventData.length == 1) {
            recordEvents.put(key, objectEncoder.encode(eventData[0]));
        }
        recordEvents.put(key, objectEncoder.encode(eventData));
    }

    public List<KeyValue> getUpsertStateExecutionLocalAttributes() {
        return upsertAttributesToReturnToServer.entrySet().stream()
                .map(stringEncodedObjectEntry ->
                        new KeyValue()
                                .key(stringEncodedObjectEntry.getKey())
                                .value(stringEncodedObjectEntry.getValue()))
                .collect(Collectors.toList());
    }

    public List<KeyValue> getRecordEvents() {
        return recordEvents.entrySet().stream()
                .map(stringEncodedObjectEntry ->
                        new KeyValue()
                                .key(stringEncodedObjectEntry.getKey())
                                .value(stringEncodedObjectEntry.getValue()))
                .collect(Collectors.toList());
    }
}
