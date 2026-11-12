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
const escapeSequencePattern = /\\u([0-9a-fA-F]{4})|\\x([0-9a-fA-F]{2})/g;
const privateKeyBlockPattern = /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/g;
const urlUserInfoPattern = /([A-Za-z][A-Za-z0-9+.-]*:\/\/)([^ \t\n\r\f\v/:@'"]+):([^ \t\n\r\f\v/@'"]+)@/g;
const userFlagPattern = /(^|[ \t\n\r\f\v])(-u|--user|--proxy-user)([ \t]+|=)/g;
const namedValuePattern = /(["']?)([A-Za-z_][A-Za-z0-9_.-]*)(["']?)([ \t]*)([:=])([ \t]*)/g;
const flagValuePattern = /(^|[ \t\n\r\f\v])(--?)([A-Za-z][A-Za-z0-9_-]*)([ \t]+)/g;
const standaloneSchemePattern = /\b(bearer|basic|digest)([ \t]+)([A-Za-z0-9._~+/-]+=*)/gi;
const tokenShapedPattern = /[0-9+/=._~-]/;
const authorizationSchemePattern = /^(bearer|basic|digest|token|negotiate)[ \t]+/i;
const tokenShapesPattern =
  /\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,}|glpat-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})/g;
const bareValueStops = new Set([" ", "\t", "\n", "\r", "\f", "\v", "&", ";", ",", "}", "]", ")"]);

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
 * It first decodes `\uXXXX` and `\xXX` escapes, so an escaped name such as `pass\u0077ord` is found; the returned
 * text keeps them decoded. It then replaces private key blocks; the password of a URL such as
 * `https://user:pass@host`; the password of `-u user:pass`, `--user`, and `--proxy-user`; and the whole value after
 * a name that {@link isCredentialLikeName} matches, in a `name=value` assignment, a `name: value` header or field, a
 * JSON member, or a `--name value` flag, which covers query parameters such as `?token=…`. A value ends at its
 * closing quote, escaped quotes included, or at whitespace, `&`, `;`, `,`, `}`, `]`, or `)`; the value of an
 * `Authorization` or `Cookie` header runs to the end of its line, and its scheme, such as `Bearer`, stays. A value
 * that is exactly {@link redactedArgumentValue} stays. Outside a header, a token-shaped word after `Bearer`, `Basic`,
 * or `Digest` is replaced, and so are common token shapes, such as AWS access key IDs and GitHub, Slack, GitLab, and
 * OpenAI-style keys and JWTs. The rules are conservative: a generic `key=value` is replaced too. They cannot find
 * every credential: a value in an unusual form stays as it is. The Python core applies the same rules; the shared
 * cases are in `agents/conformance/approval_display.json`.
 *
 * @example
 * ```ts
 * redactInlineCredentials('curl -H "Authorization: Bearer t1" https://api.example.com?token=t1');
 * // 'curl -H "Authorization: Bearer <redacted>" https://api.example.com?token=<redacted>'
 * ```
 * @param text - The text.
 * @returns The text with escapes decoded and each credential found replaced.
 * @since 0.6.0
 */
export function redactInlineCredentials(text: string): string {
  let redacted = text.replace(escapeSequencePattern, decodeEscapeSequence);
  redacted = redacted.replace(privateKeyBlockPattern, () => redactedArgumentValue);
  redacted = redacted.replace(
    urlUserInfoPattern,
    (_match, scheme: string, user: string) => `${scheme}${user}:${redactedArgumentValue}@`,
  );
  redacted = redactValuesAfter(redacted, userFlagPattern, () => "user");
  redacted = redactValuesAfter(redacted, namedValuePattern, (match, valueStart, scanned) => {
    const isComparison = match[5] === "=" && scanned[valueStart] === "=";
    if (isComparison || !isCredentialLikeName(match[2]!)) {
      return null;
    }
    return isHeaderName(match[2]!) ? "header" : "value";
  });
  redacted = redactValuesAfter(redacted, flagValuePattern, (match, valueStart, scanned) =>
    isCredentialLikeName(match[3]!) && scanned[valueStart] !== "-" ? "value" : null,
  );
  redacted = redacted.replace(standaloneSchemePattern, (match: string, scheme: string, gap: string, value: string) =>
    tokenShapedPattern.test(value) ? `${scheme}${gap}${redactedArgumentValue}` : match,
  );
  return redacted.replace(tokenShapesPattern, () => redactedArgumentValue);
}

type ValueKind = "value" | "header" | "user";

/** Replaces the value after each match the kind function accepts, scanning left to right. */
function redactValuesAfter(
  text: string,
  pattern: RegExp,
  kindOf: (match: RegExpExecArray, valueStart: number, scanned: string) => ValueKind | null,
): string {
  let output = "";
  let position = 0;
  for (;;) {
    pattern.lastIndex = position;
    const match = pattern.exec(text);
    if (match === null) {
      break;
    }
    const valueStart = match.index + match[0].length;
    const kind = kindOf(match, valueStart, text);
    if (kind === null) {
      output += text.slice(position, valueStart);
      position = valueStart;
      continue;
    }
    // A quote right before a name, with none after it, is the quote the value ends at.
    const hasOuterQuote = pattern === namedValuePattern && match[1] !== "" && match[3] === "";
    const valueEnd = findValueEnd(text, valueStart, kind === "header", hasOuterQuote ? match[1]! : "\u0000");
    output += text.slice(position, valueStart) + redactValue(text.slice(valueStart, valueEnd), kind);
    position = valueEnd;
  }
  return output + text.slice(position);
}

/**
 * Where a value starting at `start` ends: its closing quote, a stop character, or, for a header, the line end. An
 * `outerQuote` opened before the name also ends it; another quote continues it, skipping a quoted part that closes.
 */
function findValueEnd(text: string, start: number, isHeader: boolean, outerQuote: string): number {
  if (isQuote(text[start])) {
    return findQuotedEnd(text, start);
  }
  let index = start;
  while (index < text.length && text[index] !== outerQuote) {
    const character = text[index]!;
    if (isHeader ? character === "\n" || character === "\r" : bareValueStops.has(character)) {
      break;
    }
    const quotedEnd = isQuote(character) ? findQuotedEnd(text, index) : index + 1;
    index = quotedEnd - 1 > index && text[quotedEnd - 1] === character ? quotedEnd : index + 1;
  }
  return index;
}

/** The index after the quote that closes the one at `start`, skipping escaped double quotes, or the line end. */
function findQuotedEnd(text: string, start: number): number {
  const quote = text[start];
  let index = start + 1;
  while (index < text.length && text[index] !== quote && text[index] !== "\n") {
    index += quote === '"' && text[index] === "\\" ? 2 : 1;
  }
  return Math.min(index < text.length && text[index] === quote ? index + 1 : index, text.length);
}

function redactValue(value: string, kind: ValueKind): string {
  if (value === "") {
    return value;
  }
  const quote = isQuote(value[0]) ? value[0]! : "";
  const isClosed = quote !== "" && value.length >= 2 && value.endsWith(quote);
  const inner = quote === "" ? value : value.slice(1, isClosed ? -1 : undefined);
  const closing = isClosed ? quote : "";
  if (kind === "user") {
    const separator = inner.indexOf(":");
    if (separator < 0 || inner.slice(separator + 1) === redactedArgumentValue) {
      return value;
    }
    return `${quote}${inner.slice(0, separator)}:${redactedArgumentValue}${closing}`;
  }
  if (inner === redactedArgumentValue) {
    return value;
  }
  const scheme = kind === "header" && quote === "" ? authorizationSchemePattern.exec(inner) : null;
  if (scheme !== null) {
    return inner.slice(scheme[0].length) === redactedArgumentValue ? value : `${scheme[0]}${redactedArgumentValue}`;
  }
  return `${quote}${redactedArgumentValue}${closing}`;
}

function isHeaderName(name: string): boolean {
  const words = name.toLowerCase().split(/[^a-z0-9]+/);
  return words.includes("authorization") || words.includes("cookie") || words.includes("cookies");
}

function isQuote(character: string | undefined): boolean {
  return character === '"' || character === "'";
}

function decodeEscapeSequence(match: string, unicode: string | undefined, byte: string | undefined): string {
  const code = Number.parseInt(unicode ?? byte ?? "", 16);
  // A lone surrogate stays escaped, so the text stays valid Unicode.
  return code >= 0xd800 && code <= 0xdfff ? match : String.fromCharCode(code);
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
