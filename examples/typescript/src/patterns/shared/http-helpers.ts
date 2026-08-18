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

import type { UserProfile, UserProfileRequest } from "../entity-store/user-profile.js";

export function requiredString(value: unknown, name: string): string {
  const result = String(value ?? "").trim();
  if (result.length === 0) {
    throw new Error(`${name} is required`);
  }
  return result;
}

export function profileFromRequest(request: UserProfileRequest): UserProfile {
  if (typeof request.marketingOptIn !== "boolean") {
    throw new Error("marketingOptIn must be a boolean");
  }
  return {
    displayName: requiredString(request.displayName, "displayName"),
    email: requiredString(request.email, "email"),
    marketingOptIn: request.marketingOptIn,
    credits: requiredSafeInteger(request.credits, "credits"),
    weight: requiredFiniteNumber(request.weight, "weight"),
    lastLoggedInTime: requiredDate(request.lastLoggedInTime, "lastLoggedInTime"),
    metadata: request.metadata,
  };
}

function requiredSafeInteger(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) {
    throw new Error(`${name} must be a safe integer`);
  }
  return value;
}

function requiredFiniteNumber(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${name} must be a finite number`);
  }
  return value;
}

function requiredDate(value: unknown, name: string): Date {
  const result = new Date(String(value ?? ""));
  if (Number.isNaN(result.getTime())) {
    throw new Error(`${name} must be an ISO datetime`);
  }
  return result;
}
