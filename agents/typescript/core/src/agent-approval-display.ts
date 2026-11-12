// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentInterruptDisplay } from "./agent-session-values.js";

/**
 * Largest {@link AgentInterruptDisplay.preview} the session stores, in UTF-8 bytes: 16 KiB. The session drops a
 * longer preview and keeps the target and summary, because the display is stored on the call's record, on its
 * interrupt, and in its `interrupt_raised` event.
 * @since 0.6.0
 */
export const maxApprovalPreviewBytes = 16_384;

/**
 * The text an {@link AgentInterruptDisplay} shows instead of the value of a credential-like argument.
 * @since 0.6.0
 */
export const redactedArgumentValue = "<redacted>";

const credentialNames: ReadonlySet<string> = new Set([
  "api_key",
  "apikey",
  "key",
  "token",
  "access_token",
  "refresh_token",
  "id_token",
  "secret",
  "client_secret",
  "password",
  "authorization",
  "proxy_authorization",
  "cookie",
  "set_cookie",
]);
const credentialNameSuffixes = ["_token", "_secret", "_key", "_password"] as const;
const maxDisplayedArguments = 5;
const maxDisplayedArgumentCharacters = 80;

/**
 * Whether an argument, header, or field name looks like it holds a credential, so a display shows its value as
 * {@link redactedArgumentValue}.
 *
 * The check ignores case and treats `-` and camelCase boundaries as `_`. It matches `api_key`, `apikey`, `key`,
 * `token`, `access_token`, `refresh_token`, `id_token`, `secret`, `client_secret`, `password`, `authorization`,
 * `proxy_authorization`, `cookie`, and `set_cookie`, and every name that ends in `_token`, `_secret`, `_key`, or
 * `_password`. So `X-Api-Key`, `clientSecret`, and `client_secret` all match. The Python core applies the same rule.
 *
 * @example
 * ```ts
 * isCredentialLikeName("clientSecret"); // true
 * isCredentialLikeName("channel"); // false
 * ```
 * @param name - The name to check.
 * @returns `true` when the name looks like it holds a credential.
 * @since 0.6.0
 */
export function isCredentialLikeName(name: string): boolean {
  const words = name.replace(/(?<=[a-z0-9])(?=[A-Z])/g, "_").toLowerCase().replaceAll("-", "_");
  return credentialNames.has(words) || credentialNameSuffixes.some((suffix) => words.endsWith(suffix));
}

/**
 * The connector, connection, operation, and arguments of one connector tool call, for
 * {@link buildConnectorToolApprovalDisplay}.
 * @since 0.6.0
 */
export interface ConnectorToolApprovalDisplayInput {
  /** The Connector ID, such as `slack`. */
  readonly connectorId: string;
  /** The connection's name, such as `support-slack`. */
  readonly connectionName: string;
  /** The operation the call runs, such as `postChannelMessage`. */
  readonly operation: string;
  /** The call's arguments as JSON text. */
  readonly arguments: string;
}

/**
 * Builds the approval display of one connector tool call from its operation, its connection, and its short
 * arguments.
 *
 * The target is `<connectorId>/<connectionName>`. The summary names the operation and the connection and lists up to
 * five top-level arguments in argument order: each string of at most 80 characters on one line, number, and boolean,
 * as JSON. A credential-like argument (see {@link isCredentialLikeName}) shows {@link redactedArgumentValue} instead
 * of its value. Longer text, objects, and lists are left out; the approval UI shows them from the arguments. The
 * summary never uses the operation's description, which says what the operation is for, not what this call does.
 * `ConnectorToolSource` and the CLI's connector tools use it; a source of other connector tools may too.
 *
 * @example
 * ```ts
 * const display = buildConnectorToolApprovalDisplay({
 *   connectorId: "slack",
 *   connectionName: "support-slack",
 *   operation: "postChannelMessage",
 *   arguments: '{"channel":"#eng","text":"Deploy is done"}',
 * });
 * // display.summary: 'Calls slack postChannelMessage with connection support-slack; channel: "#eng", text: "Deploy is done".'
 * ```
 * @param input - The call's connector, connection, operation, and arguments.
 * @returns The display, with no preview.
 * @since 0.6.0
 */
export function buildConnectorToolApprovalDisplay(input: ConnectorToolApprovalDisplayInput): AgentInterruptDisplay {
  const keyArguments = describeKeyArguments(input.arguments);
  const head = `Calls ${input.connectorId} ${input.operation} with connection ${input.connectionName}`;
  return {
    target: `${input.connectorId}/${input.connectionName}`,
    summary: keyArguments.length === 0 ? `${head}.` : `${head}; ${keyArguments.join(", ")}.`,
    preview: null,
  };
}

function describeKeyArguments(argumentsText: string): string[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsText);
  } catch {
    return [];
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return [];
  }
  const described: string[] = [];
  for (const [name, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (described.length === maxDisplayedArguments) {
      break;
    }
    if (isCredentialLikeName(name)) {
      described.push(`${name}: ${redactedArgumentValue}`);
    } else if (isShortScalar(value)) {
      described.push(`${name}: ${JSON.stringify(value)}`);
    }
  }
  return described;
}

function isShortScalar(value: unknown): boolean {
  if (typeof value === "string") {
    return !/[\r\n]/.test(value) && Array.from(value).length <= maxDisplayedArgumentCharacters;
  }
  return typeof value === "boolean" || (typeof value === "number" && Number.isFinite(value));
}

/**
 * Returns the display the session stores for a call: `null` for none or one that is not a display, and the display
 * without its preview when the preview is over {@link maxApprovalPreviewBytes}.
 */
export function boundApprovalDisplay(display: unknown): AgentInterruptDisplay | null {
  if (typeof display !== "object" || display === null) {
    return null;
  }
  const { target, summary, preview } = display as Partial<Record<keyof AgentInterruptDisplay, unknown>>;
  if (typeof target !== "string" || typeof summary !== "string") {
    return null;
  }
  const isPreviewKept =
    typeof preview === "string" && new TextEncoder().encode(preview).length <= maxApprovalPreviewBytes;
  return { target, summary, preview: isPreviewKept ? preview : null };
}
