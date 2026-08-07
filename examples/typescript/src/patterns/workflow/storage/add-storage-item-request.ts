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

export interface AddStorageItemRequest {
  key: string;
  value: string;
}

export const addStorageItemRequestCodec = {
  typeName: "AddStorageItemRequest",
  decode: (value: unknown): AddStorageItemRequest => {
    const record = value as AddStorageItemRequest;
    if (record.key === undefined || record.key === null) {
      throw new Error("key is null");
    }
    if (record.value === undefined || record.value === null) {
      throw new Error("value is null");
    }
    return {
      key: String(record.key),
      value: String(record.value),
    };
  },
};
