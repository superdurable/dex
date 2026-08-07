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

package io.superdurable.dex.patterns.workflow.storage;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.StepList;
import org.springframework.stereotype.Component;

/** A singleton flow that acts as storage. Limited to 4MB storage. */
@Component
public class StorageFlow implements Flow<Void> {
    private static final String DA_STORE = "Store";

    public final Attribute<Storage> store = Attribute.define(DA_STORE, Storage.class);

    public static String getStorageFlowId() {
        return String.format("sample-storage-%s", "test");
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.empty();
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(store);
    }

    @RPC(lockAttributes = {DA_STORE})
    public void addItem(final Context context, final AddStorageItemRequest request) {
        Storage storage = store.get(context);
        if (storage == null) {
            storage = new Storage();
        }
        storage.addItem(request.key, request.value);
        store.set(context, storage);
    }

    @RPC
    public RPCResult<String> getItem(final Context context, final String itemKey) {
        final Storage storage = store.get(context);
        return RPCResult.of(storage == null ? null : storage.getItem(itemKey));
    }

    @RPC(lockAttributes = {DA_STORE})
    public void removeItem(final Context context, final String itemKey) {
        final Storage storage = store.get(context);
        if (storage != null) {
            storage.removeItem(itemKey);
            store.set(context, storage);
        }
    }
}
