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
 * Largest {@link AgentInterruptDisplay.target} the session stores, in UTF-8 bytes: 1 KiB. The session cuts a longer
 * target and ends it with `…`.
 * @since 0.6.0
 */
export const maxApprovalTargetBytes = 1_024;

/**
 * Largest {@link AgentInterruptDisplay.summary} the session stores, in UTF-8 bytes: 2 KiB. The session cuts a longer
 * summary and ends it with `…`.
 * @since 0.6.0
 */
export const maxApprovalSummaryBytes = 2_048;

/**
 * The text an {@link AgentInterruptDisplay} shows instead of a credential.
 * @since 0.6.0
 */
export const redactedArgumentValue = "<redacted>";

const truncationMark = "…";
const credentialWords: ReadonlySet<string> = new Set([
  "apikey",
  "apikeys",
  "auth",
  "authorization",
  "bearer",
  "cookie",
  "cookies",
  "cred",
  "creds",
  "credential",
  "credentials",
  "dsn",
  "jwt",
  "key",
  "otp",
  "pass",
  "passphrase",
  "passwd",
  "password",
  "passwords",
  "privatekey",
  "pwd",
  "secret",
  "secrets",
  "session",
  "sessionid",
  "signature",
  "token",
  "tokens",
]);
const credentialWordEndings = [
  "apikey",
  "credential",
  "credentials",
  "passwd",
  "password",
  "secret",
  "secrets",
  "token",
  "tokens",
] as const;
const credentialPhrases: readonly (readonly string[])[] = [
  ["connection", "string"],
  ["conn", "str"],
];
const maxDisplayedArguments = 5;
const maxDisplayedArgumentCharacters = 80;

// Explicit ASCII classes keep the patterns identical to the Python core's, which compiles them with re.ASCII.
const space = "[ \\t\\n\\r\\f\\v]";
const quotedOrBareValue = `("[^"]*"|'[^']*'|[^ \\t\\n\\r\\f\\v"'&;,=][^ \\t\\n\\r\\f\\v"'&;,]*)`;
const inlineCredentialRules: readonly {
  readonly pattern: RegExp;
  readonly replace: (match: string, ...groups: string[]) => string;
}[] = [
  {
    pattern: /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/g,
    replace: () => redactedArgumentValue,
  },
  {
    pattern: /([A-Za-z][A-Za-z0-9+.-]*:\/\/)([^ \t\n\r\f\v/:@'"]+):([^ \t\n\r\f\v/@'"]+)@/g,
    replace: (_match, scheme, user) => `${scheme}${user}:${redactedArgumentValue}@`,
  },
  {
    pattern: new RegExp(`\\b(bearer|basic|digest)(${space}+)([A-Za-z0-9._~+/=-]{6,})`, "gi"),
    replace: (_match, scheme, gap) => `${scheme}${gap}${redactedArgumentValue}`,
  },
  {
    pattern: new RegExp(`(^|${space})(-u|--user|--proxy-user)(${space}+|=)(["']?)([^ \\t\\n\\r\\f\\v:"']+):([^ \\t\\n\\r\\f\\v"']+)`, "g"),
    replace: (_match, start, flag, gap, quote, user) => `${start}${flag}${gap}${quote}${user}:${redactedArgumentValue}`,
  },
  {
    pattern: new RegExp(`([A-Za-z_][A-Za-z0-9_.-]*)(${space}*=${space}*)${quotedOrBareValue}`, "g"),
    replace: (match, name, separator, value) => redactNamedValue(match, name, `${name}${separator}`, value),
  },
  {
    pattern: new RegExp(`(["']?)([A-Za-z][A-Za-z0-9_.-]*)(["']?${space}*:${space}*)${quotedOrBareValue}`, "g"),
    replace: (match, quote, name, separator, value) => redactNamedValue(match, name, `${quote}${name}${separator}`, value),
  },
  {
    pattern: new RegExp(`(--?)([A-Za-z][A-Za-z0-9_-]*)(${space}+)("[^"]*"|'[^']*'|[^ \\t\\n\\r\\f\\v"'-][^ \\t\\n\\r\\f\\v"']*)`, "g"),
    replace: (match, dashes, name, gap, value) => redactNamedValue(match, name, `${dashes}${name}${gap}`, value),
  },
  {
    pattern:
      /\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,}|glpat-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})/g,
    replace: () => redactedArgumentValue,
  },
];

/**
 * Whether an argument, header, field, or variable name looks like it holds a credential, so a display shows its
 * value as {@link redactedArgumentValue}.
 *
 * The name is split into words at `_`, `-`, `.`, spaces, and camelCase boundaries, ignoring case. It is a credential
 * name when a word is one of `apikey`, `auth`, `authorization`, `bearer`, `cookie`, `cred`, `credential`, `dsn`,
 * `jwt`, `key`, `otp`, `pass`, `passphrase`, `passwd`, `password`, `privatekey`, `pwd`, `secret`, `session`,
 * `sessionid`, `signature`, or `token` (and the plural forms `apikeys`, `cookies`, `creds`, `credentials`,
 * `passwords`, `secrets`, and `tokens`), when a word ends in `apikey`, `credential`, `credentials`, `passwd`,
 * `password`, `secret`, `secrets`, `token`, or `tokens`, as in `PGPASSWORD`, or when it holds the words
 * `connection string` or `conn str`. So
 * `X-Api-Key`, `clientSecret`, `private_key`, `credentials`, and `DATABASE_CONNECTION_STRING` all match, and
 * `channel`, `monkey`, `tokenizer`, and `author` do not. The Python core applies the same rule; the shared cases
 * are in `agents/conformance/approval_display.json`.
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
  const words = name
    .replace(/(?<=[a-z0-9])(?=[A-Z])/g, "_")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((word) => word !== "");
  return (
    words.some((word) => credentialWords.has(word) || credentialWordEndings.some((ending) => word.endsWith(ending))) ||
    credentialPhrases.some((phrase) =>
      words.some((_word, index) => phrase.every((part, offset) => words[index + offset] === part)),
    )
  );
}

/**
 * Replaces the credentials written inline in a text, such as a shell command or a file, with
 * {@link redactedArgumentValue}.
 *
 * It replaces private key blocks; the password of a URL such as `https://user:pass@host`; the value after `Bearer`,
 * `Basic`, or `Digest`; the password of `-u user:pass`, `--user`, and `--proxy-user`; the value of a `name=value`
 * assignment, a `name: value` header or field, and a `--name value` flag whose name {@link isCredentialLikeName}
 * matches, which covers query parameters such as `?token=…`; and common token shapes, such as AWS access key IDs and
 * GitHub, Slack, GitLab, and OpenAI-style keys and JWTs. A quoted value is replaced whole. It cannot find every
 * credential: a value in an unusual form stays as it is. The Python core applies the same rules; the shared cases
 * are in `agents/conformance/approval_display.json`.
 *
 * @example
 * ```ts
 * redactInlineCredentials('curl -H "Authorization: Bearer abc123456" https://api.example.com?token=t1');
 * // 'curl -H "Authorization: Bearer <redacted>" https://api.example.com?token=<redacted>'
 * ```
 * @param text - The text.
 * @returns The text with each credential found replaced.
 * @since 0.6.0
 */
export function redactInlineCredentials(text: string): string {
  return inlineCredentialRules.reduce(
    (redacted, rule) => redacted.replace(rule.pattern, (match: string, ...groups: unknown[]) => rule.replace(match, ...(groups as string[]))),
    text,
  );
}

function redactNamedValue(match: string, name: string, prefix: string, value: string): string {
  if (!isCredentialLikeName(name) || value.includes(redactedArgumentValue) || /^(bearer|basic|digest)$/i.test(value)) {
    return match;
  }
  const quote = value.startsWith('"') || value.startsWith("'") ? value[0]! : "";
  return `${prefix}${quote}${redactedArgumentValue}${quote}`;
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
 * of its value, and a string value has its inline credentials replaced (see {@link redactInlineCredentials}). Longer
 * text, objects, and lists are left out; the approval UI shows them from the arguments. The summary never uses the
 * operation's description, which says what the operation is for, not what this call does. `ConnectorToolSource` and
 * the CLI's connector tools use it; a source of other connector tools may too.
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
      described.push(`${name}: ${JSON.stringify(typeof value === "string" ? redactInlineCredentials(value) : value)}`);
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
 * Returns the display the session stores for a call: `null` for none or one that is not a display; otherwise the
 * target and summary cut to {@link maxApprovalTargetBytes} and {@link maxApprovalSummaryBytes}, and the preview, or
 * `null` when it is over {@link maxApprovalPreviewBytes}.
 */
export function boundApprovalDisplay(display: unknown): AgentInterruptDisplay | null {
  if (typeof display !== "object" || display === null) {
    return null;
  }
  const { target, summary, preview } = display as Partial<Record<keyof AgentInterruptDisplay, unknown>>;
  if (typeof target !== "string" || typeof summary !== "string") {
    return null;
  }
  const isPreviewKept = typeof preview === "string" && utf8Length(preview) <= maxApprovalPreviewBytes;
  return {
    target: truncateUtf8(target, maxApprovalTargetBytes),
    summary: truncateUtf8(summary, maxApprovalSummaryBytes),
    preview: isPreviewKept ? preview : null,
  };
}

/** The text, or its longest prefix of whole code points that fits `maxBytes` with `…` appended. */
function truncateUtf8(text: string, maxBytes: number): string {
  if (utf8Length(text) <= maxBytes) {
    return text;
  }
  const budget = maxBytes - utf8Length(truncationMark);
  let kept = "";
  let used = 0;
  for (const character of text) {
    const size = utf8Length(character);
    if (used + size > budget) {
      break;
    }
    kept += character;
    used += size;
  }
  return `${kept}${truncationMark}`;
}

function utf8Length(text: string): number {
  return new TextEncoder().encode(text).length;
}
