// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";

const uuidV7Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
/** 2000-01-01T00:00:00Z. */
const earliestKeyTimeMs = 946_684_800_000;
/** 9999-12-31T23:59:59.999Z. */
const latestKeyTimeMs = 253_402_300_799_999;
const shortSuffixLength = 6;
const longSuffixLength = 12;

/**
 * Returns the Unix time in milliseconds of a terminal UI idempotency key, or `undefined` when the key is not a
 * lowercase, hyphenated UUIDv7 whose time lies in the years 2000 to 9999.
 */
export function idempotencyKeyTimeMs(idempotencyKey: string): number | undefined {
  if (!uuidV7Pattern.test(idempotencyKey)) {
    return undefined;
  }
  const timeMs = Number.parseInt(idempotencyKey.slice(0, 8) + idempotencyKey.slice(9, 13), 16);
  return timeMs >= earliestKeyTimeMs && timeMs <= latestKeyTimeMs ? timeMs : undefined;
}

/**
 * Returns the two session IDs `start_session` tries for a key, in order: `<harness>-<YYYYMMDD>-<HHMMSS>-<hex6>`
 * and the same with 12 hex digits. The time is the key's, floored to the second, in UTC.
 *
 * @throws {RangeError} When the key is not a valid idempotency key.
 */
export function candidateSessionIds(harness: string, idempotencyKey: string): readonly [string, string] {
  const timeMs = idempotencyKeyTimeMs(idempotencyKey);
  if (timeMs === undefined) {
    throw new RangeError("the idempotency key is not a lowercase UUIDv7");
  }
  const stamp = new Date(Math.floor(timeMs / 1000) * 1000)
    .toISOString()
    .replace(/[-:]/g, "")
    .replace(/\..*$/, "")
    .replace("T", "-");
  const digest = createHash("sha256").update(idempotencyKey, "ascii").digest("hex");
  return [
    `${harness}-${stamp}-${digest.slice(0, shortSuffixLength)}`,
    `${harness}-${stamp}-${digest.slice(0, longSuffixLength)}`,
  ];
}
