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
  readonly credits: number;
  readonly weight: number;
  readonly lastLoggedInTime: Date;
  readonly metadata: UserProfileMetadata;
}

export interface UserProfileMetadata {
  readonly source: string;
  readonly tags: readonly string[];
}

export interface UserProfileRequest {
  readonly userId: string;
  readonly displayName: string;
  readonly email: string;
  readonly marketingOptIn: boolean;
  readonly credits: number;
  readonly weight: number;
  readonly lastLoggedInTime: string;
  readonly metadata: UserProfileMetadata;
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
  const credits = record.credits;
  if (typeof credits !== "number" || !Number.isSafeInteger(credits)) {
    throw new Error("credits must be a safe integer");
  }
  if (typeof record.weight !== "number" || !Number.isFinite(record.weight)) {
    throw new Error("weight must be a finite number");
  }
  const lastLoggedInTime = record.lastLoggedInTime instanceof Date
    ? record.lastLoggedInTime
    : new Date(String(record.lastLoggedInTime ?? ""));
  if (Number.isNaN(lastLoggedInTime.getTime())) {
    throw new Error("lastLoggedInTime must be an ISO datetime");
  }
  const metadata = decodeMetadata(record.metadata);
  return {
    displayName,
    email,
    marketingOptIn: record.marketingOptIn,
    credits,
    weight: record.weight,
    lastLoggedInTime,
    metadata,
  };
}

export const userProfileCodec = jsonCodec<UserProfile>({
  typeName: "UserProfile",
  decode: decodeUserProfile,
  encode: (profile) => ({
    ...profile,
    lastLoggedInTime: profile.lastLoggedInTime.toISOString(),
  }),
});

export const dateCodec = jsonCodec<Date>({
  typeName: "Date",
  decode: (value) => {
    const date = new Date(String(value));
    if (Number.isNaN(date.getTime())) {
      throw new TypeError("Date requires an ISO datetime string");
    }
    return date;
  },
  encode: (value) => value.toISOString(),
});

export const metadataCodec = jsonCodec<UserProfileMetadata>({
  typeName: "UserProfileMetadata",
  decode: decodeMetadata,
});

function decodeMetadata(value: unknown): UserProfileMetadata {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("metadata must be an object");
  }
  const record = value as Partial<UserProfileMetadata>;
  const source = String(record.source ?? "").trim();
  if (source.length === 0) {
    throw new Error("metadata.source is required");
  }
  if (!Array.isArray(record.tags) || !record.tags.every((tag) => typeof tag === "string")) {
    throw new Error("metadata.tags must be an array of strings");
  }
  return { source, tags: [...record.tags] };
}
