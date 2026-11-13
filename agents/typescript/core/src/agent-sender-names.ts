// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The most Unicode code points `AgentUserMessage.senderName` may have.
 *
 * @since 0.6.2
 */
export const maxSenderNameLength = 64;

// Format characters (Cf) can reverse or hide a label, and a lone surrogate (Cs) does not round-trip.
const refusedSenderNameCharacter = /[\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}]/u;
const spaceSeparator = /^\p{Zs}$/u;

/**
 * Reports whether a declared sender display name is valid.
 *
 * `send_user_message` and `steer_user_message` refuse a message whose `senderName` fails this check. Check a name a
 * person typed with it before you send it, after you trim it. Every core applies the same rule.
 *
 * @example
 * ```ts
 * isValidSenderName("Alice"); // true
 * isValidSenderName(" Alice"); // false
 * ```
 * @param senderName - The name to check.
 * @returns `true` when the name has 1 to {@link maxSenderNameLength} code points, contains no control character
 * (Unicode category `Cc`), no format character such as a bidirectional override or a zero-width character (`Cf`), no
 * lone surrogate (`Cs`), and no line or paragraph separator (`Zl`, `Zp`), and neither starts nor ends with a space
 * separator (`Zs`); `false` otherwise.
 * @since 0.6.2
 */
export function isValidSenderName(senderName: string): boolean {
  const codePoints = [...senderName];
  if (codePoints.length < 1 || codePoints.length > maxSenderNameLength) {
    return false;
  }
  if (refusedSenderNameCharacter.test(senderName)) {
    return false;
  }
  return !spaceSeparator.test(codePoints[0] ?? "") && !spaceSeparator.test(codePoints[codePoints.length - 1] ?? "");
}

/**
 * Returns the text the model sees for one user message.
 *
 * The session prefixes a message that has a sender name with that name, so the model knows who speaks when several
 * people use one session.
 *
 * @example
 * ```ts
 * buildSenderNamedText("Alice", "hello"); // "Alice: hello"
 * ```
 * @param senderName - `AgentUserMessage.senderName`, or `undefined`.
 * @param text - The message text.
 * @returns `"<senderName>: <text>"` when `senderName` is set, and `text` unchanged when it is `undefined`.
 * @since 0.6.2
 */
export function buildSenderNamedText(senderName: string | undefined, text: string): string {
  return senderName === undefined ? text : `${senderName}: ${text}`;
}
