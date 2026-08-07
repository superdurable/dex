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

import {
  AttributeMap,
  StepList,
  jsonCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "@superdurable/dex";

import {
  addStorageItemRequestCodec,
  type AddStorageItemRequest,
} from "./add-storage-item-request.js";

const DA_STORE = "Store";
const addStorageItemInputCodec = jsonCodec<AddStorageItemRequest>(addStorageItemRequestCodec);

export const STORAGE_FLOW_ID = "sample-storage-test";

export class StorageFlow implements Flow<void> {
  public readonly store = new AttributeMap(DA_STORE, stringCodec);

  public static getStorageFlowId(): string {
    return STORAGE_FLOW_ID;
  }

  public getFlowType(): string {
    return "StorageFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.store] };
  }

  @rpc({ inputCodec: addStorageItemInputCodec })
  public addItem(context: Context, request: AddStorageItemRequest): void {
    this.store.set(context, request.key, request.value);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public getItem(context: Context, itemKey: string): RPCResult<string> {
    return { output: this.store.get(context, itemKey) };
  }

  @rpc({ inputCodec: stringCodec })
  public removeItem(context: Context, itemKey: string): void {
    this.store.delete(context, itemKey);
  }
}

export const storageFlow = new StorageFlow();
