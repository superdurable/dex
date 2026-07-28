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

package io.iworkflow.patterns.workflow.storage;

import java.util.HashMap;
import java.util.Map;

/**
 * Limited to 4MB storage
 * @param storageData the map of key-value pairs acting as the stored data
 */
public record Storage(Map<String, String> storageData) {
    // Default map to empty
    public Storage() {
        this(new HashMap<>());
    }

    // Replace null map with empty
    public Storage(Map<String, String> storageData) {
        this.storageData = storageData == null ? new HashMap<>() : storageData;
    }

    /**
     * Add a key-value pair to the storage
     * @param key the key to add
     * @param value the value to add
     */
    public void addItem(String key, String value) {
        storageData.put(key, value);
    }

    /**
     * Gets the value for the given key
     * @param key the key to get the value for
     * @return the value for the given key, or null if the key is not found
     */
    public String getItem(String key) {
        return storageData.get(key);
    }

    /**
     * Removes the key-value pair from the storage
     * @param key the key to remove
     */
    public void removeItem(String key) {
        storageData.remove(key);
    }
}
