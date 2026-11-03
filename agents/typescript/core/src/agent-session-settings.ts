// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { SessionSettings } from "./agent-session-values.js";

/** Largest {@link SessionSettings}, as the UTF-8 bytes of their compact JSON text: 16 KiB. Same in every core. */
export const MAX_SESSION_SETTINGS_BYTES = 16 * 1024;

/**
 * Extra bytes the session's own Steps and RPC accept over {@link MAX_SESSION_SETTINGS_BYTES}.
 *
 * The cores write some numbers and characters differently, so settings that one core's Client accepted can
 * measure a few bytes more in another core's Worker. The tolerance keeps that from failing a session.
 * {@link validateSessionSettings} checks the exact limit.
 */
export const SESSION_SETTINGS_SIZE_TOLERANCE_BYTES = 1024;

const utf8Encoder = new TextEncoder();

/**
 * Returns the size of session settings that {@link MAX_SESSION_SETTINGS_BYTES} limits.
 *
 * The size is the UTF-8 byte length of `JSON.stringify(sessionSettings)`: compact, with characters outside ASCII
 * written as themselves. The Python and Go cores measure their own JSON text, which is the same for strings,
 * integers, booleans, `null`, objects, and arrays, with two exceptions: Python writes floats in their shortest
 * form, such as `1.0` and `1e+16`, where this core writes `1` and `10000000000000000`, and Go escapes U+2028
 * and U+2029. So sizes can differ by a few bytes per float or per such character. The session's Steps and RPC
 * accept {@link SESSION_SETTINGS_SIZE_TOLERANCE_BYTES} more than the limit, so settings that another core's
 * Client accepted never fail a session.
 *
 * @example
 * ```ts
 * sessionSettingsSize({ model: "gpt-5-mini" }); // 22
 * ```
 * @param sessionSettings - The settings.
 * @returns The size in bytes.
 * @throws {TypeError} When the settings cannot be written as JSON, such as with a cycle or a `bigint`.
 */
export function sessionSettingsSize(sessionSettings: SessionSettings): number {
  return utf8Encoder.encode(JSON.stringify(sessionSettings)).length;
}

/**
 * Checks that a value can be a session's settings: a JSON object whose compact JSON text fits in
 * {@link MAX_SESSION_SETTINGS_BYTES}.
 *
 * Call it before `Client.startFlow` or the `update_session_settings` RPC, such as in a form handler. A start whose
 * settings the session refuses fails the session at once, and the RPC returns `false`.
 * `AgentSessionTestClient.startSession` calls it.
 *
 * @example
 * ```ts
 * validateSessionSettings({ model: "gpt-5-mini" });
 * await client.startFlow(flow, sessionId, { initialMessage: null, sessionSettings: { model: "gpt-5-mini" } });
 * ```
 * @param sessionSettings - The candidate settings.
 * @throws {TypeError} When the value is not a plain JSON object or cannot be written as JSON.
 * @throws {RangeError} When its compact JSON text is larger than {@link MAX_SESSION_SETTINGS_BYTES}.
 */
export function validateSessionSettings(sessionSettings: unknown): asserts sessionSettings is SessionSettings {
  checkSessionSettings(sessionSettings, MAX_SESSION_SETTINGS_BYTES);
}

/**
 * Checks settings as the session's Steps and RPC do: {@link validateSessionSettings} with a limit of
 * {@link MAX_SESSION_SETTINGS_BYTES} plus {@link SESSION_SETTINGS_SIZE_TOLERANCE_BYTES}.
 *
 * @param sessionSettings - The candidate settings.
 * @throws {TypeError} When the value is not a plain JSON object or cannot be written as JSON.
 * @throws {RangeError} When its compact JSON text is larger than the session's limit.
 */
export function checkStoredSessionSettings(sessionSettings: unknown): asserts sessionSettings is SessionSettings {
  checkSessionSettings(sessionSettings, MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES);
}

function checkSessionSettings(sessionSettings: unknown, limitBytes: number): void {
  if (typeof sessionSettings !== "object" || sessionSettings === null || Array.isArray(sessionSettings)) {
    throw new TypeError("session settings must be a JSON object");
  }
  const size = sessionSettingsSize(sessionSettings as SessionSettings);
  if (size > limitBytes) {
    throw new RangeError(`session settings are ${size} bytes, more than the ${limitBytes} allowed`);
  }
}
