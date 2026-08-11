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

import { jsonCodec } from "@superdurable/dex";

export interface UserProfile {
  readonly displayName: string;
  readonly email: string;
  readonly marketingOptIn: boolean;
}

export interface UserProfileRequest extends UserProfile {
  readonly userId: string;
}

function decodeUserProfile(value: unknown): UserProfile {
  const record = value as Partial<UserProfile>;
  const displayName = String(record.displayName ?? "").trim();
  const email = String(record.email ?? "").trim();
  if (displayName.length === 0) {
    throw new Error("displayName is required");
  }
  if (email.length === 0) {
    throw new Error("email is required");
  }
  if (typeof record.marketingOptIn !== "boolean") {
    throw new Error("marketingOptIn must be a boolean");
  }
  return { displayName, email, marketingOptIn: record.marketingOptIn };
}

export const userProfileCodec = jsonCodec<UserProfile>({
  typeName: "UserProfile",
  decode: decodeUserProfile,
});
