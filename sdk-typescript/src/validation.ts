// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export function requireName(name: string): void {
  if (name.trim().length === 0) {
    throw new TypeError("durable name is required");
  }
}

export function requireConditionId(conditionId: string | undefined): void {
  if (conditionId !== undefined && conditionId.length === 0) {
    throw new TypeError("condition ID must not be empty");
  }
}

export function validateChannelBounds(
  atLeast: number | undefined,
  atMost: number | undefined,
): void {
  if (atLeast === undefined && atMost === undefined) {
    throw new RangeError("channel condition requires a bound");
  }
  if (atLeast !== undefined && atLeast < 0) {
    throw new RangeError("atLeast must be non-negative");
  }
  if (atMost !== undefined && atMost < 0) {
    throw new RangeError("atMost must be non-negative");
  }
  if (atLeast !== undefined && atMost !== undefined && atMost < atLeast) {
    throw new RangeError("atMost must not be below atLeast");
  }
}
