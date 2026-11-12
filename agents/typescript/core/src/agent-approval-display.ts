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
const escapeSequencePattern = /\\u([0-9a-fA-F]{4})|\\x([0-9a-fA-F]{2})|%([0-9a-fA-F]{2})/y;
const privateKeyBlockPattern = /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/dg;
const urlUserInfoPattern = /([A-Za-z][A-Za-z0-9+.-]*:\/\/)([^ \t\n\r\f\v/:@'"]+):([^ \t\n\r\f\v/@'"]+)@/dg;
const userFlagPattern = /(^|[ \t\n\r\f\v])(-u|--user|--proxy-user)([ \t]+|=)/g;
const sshpassPattern = /(^|[ \t\n\r\f\v])(sshpass[ \t]+-p)([ \t]*)/g;
const yamlBlockIndicatorPattern = /^[|>][+-]?[0-9]?[ \t]*(?:#[^\n]*)?$/;
const namedValuePattern = /(["']?)([A-Za-z_][A-Za-z0-9_.-]*)(["']?)([ \t]*)([:=])([ \t]*)/g;
const flagValuePattern = /(^|[ \t\n\r\f\v])(--?)([A-Za-z][A-Za-z0-9_-]*)([ \t]+)/g;
const standaloneSchemePattern = /\b(bearer|basic|digest)([ \t]+)([A-Za-z0-9._~+/-]+=*)/dgi;
const tokenShapedPattern = /[0-9+/=._~-]/;
const authorizationSchemePattern = /^(bearer|basic|digest|token|negotiate)[ \t]+/i;
const tokenShapesPattern =
  /\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,}|glpat-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})/dg;
const bareValueStops = new Set([" ", "\t", "\n", "\r", "\f", "\v", "&", ";", ",", "}", "]", ")"]);

/**
 * Whether an argument, header, field, or variable name looks like it holds a credential, so a display shows its
 * value as {@link redactedArgumentValue}.
 *
 * The name is split into words at `_`, `-`, `.`, and spaces, and again at camelCase boundaries, ignoring case, so
 * `pAsSwOrD` is one word. It is a credential
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
  // Whole lowercased words first, so `pAsSwOrD` matches before camelCase splits it.
  return [name, name.replace(/(?<=[a-z0-9])(?=[A-Z])/g, "_")].some((spelling) => {
    const words = spelling
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter((word) => word !== "");
    return (
      words.some((word) => credentialWords.has(word) || credentialWordEndings.some((ending) => word.endsWith(ending))) ||
      credentialPhrases.some((phrase) =>
        words.some((_word, index) => phrase.every((part, offset) => words[index + offset] === part)),
      )
    );
  });
}

/**
 * Replaces common credential shapes written inline in a text, such as a shell command or a file, with
 * {@link redactedArgumentValue}, and leaves every other character as it is.
 *
 * It is best-effort hygiene that keeps common credentials out of an approval display, not a security boundary: a
 * credential in a form it does not know, such as one spelled with Unicode lookalike letters, stays as it is, and the
 * call's arguments still hold the full text.
 *
 * It reads `\uXXXX`, `\xXX`, and `%XX` escapes as the characters they stand for, so an escaped name such as
 * `pass\u0077ord` or `%70assword` is found, but it returns the text with the escapes as written: only the characters
 * of each credential found are replaced. It replaces private key blocks; the password of a URL such as
 * `https://user:pass@host`; the password of `-u user:pass`, `--user`, `--proxy-user`, and `sshpass -p`; and the value
 * after a name that {@link isCredentialLikeName} matches, in a `name=value` assignment, a `name: value` header or
 * field, a JSON member, or a `--name value` flag, which covers query parameters such as `?token=…`. A value runs as a
 * shell word: backslash escapes, line continuations, and adjacent quoted parts join it, escaped quotes stay inside a
 * quoted part, and it ends at whitespace, `&`, `;`, `,`, `}`, `]`, or `)`; an unclosed quote runs to the end of its
 * line. A triple-quoted value runs to its closing quotes, or to the end of the text, and a YAML block scalar after
 * `name: |` or `name: >` covers its indented lines. The value of an `Authorization` or `Cookie` header runs to the end
 * of its line, with folded and continued lines, and its scheme, such as `Bearer`, stays. A value that is exactly
 * {@link redactedArgumentValue} stays. Outside a header, a token-shaped word after `Bearer`, `Basic`, or `Digest` is
 * replaced, and so are common token shapes, such as AWS access key IDs and GitHub, Slack, GitLab, and OpenAI-style
 * keys and JWTs. The rules are conservative: a generic `key=value` is replaced too. The Python core applies the same
 * rules; the shared cases are in `agents/conformance/approval_display.json`.
 *
 * @example
 * ```ts
 * redactInlineCredentials('curl -H "Authorization: Bearer t1" https://api.example.com?token=t1');
 * // 'curl -H "Authorization: Bearer <redacted>" https://api.example.com?token=<redacted>'
 * redactInlineCredentials("printf '\\x41'"); // "printf '\\x41'", unchanged
 * ```
 * @param text - The text.
 * @returns The text with each credential found replaced and nothing else changed.
 * @since 0.6.0
 */
export function redactInlineCredentials(text: string): string {
  let units = readTextUnits(text);
  for (const findSecrets of secretFinders) {
    const secrets = findSecrets(units.map((unit) => unit.text).join(""));
    if (secrets.length > 0) {
      units = replaceWithMarkers(units, secrets);
    }
  }
  return units.map((unit) => (unit.isMarker ? redactedArgumentValue : text.slice(unit.start, unit.end))).join("");
}

/**
 * One character of the text as the rules read it: an escape sequence reads as the character it stands for, and a
 * found credential reads as the marker. `start` and `end` are its span in the original text.
 */
interface TextUnit {
  readonly text: string;
  readonly start: number;
  readonly end: number;
  readonly isMarker: boolean;
}

/** A span `[start, end)` of the text the rules read that holds a credential. */
type SecretSpan = readonly [number, number];

const secretFinders: readonly ((text: string) => SecretSpan[])[] = [
  (text) => matchSpans(text, privateKeyBlockPattern, 0),
  (text) => matchSpans(text, urlUserInfoPattern, 3),
  (text) => findValueSecrets(text, userFlagPattern, () => "user"),
  (text) => findValueSecrets(text, sshpassPattern, (_match, valueStart) => (text[valueStart] === "-" ? null : "value")),
  (text) =>
    findValueSecrets(text, namedValuePattern, (match, valueStart) => {
      const isComparison = match[5] === "=" && text[valueStart] === "=";
      if (isComparison || !isCredentialLikeName(match[2]!)) {
        return null;
      }
      return isHeaderName(match[2]!) ? "header" : "value";
    }),
  (text) =>
    findValueSecrets(text, flagValuePattern, (match, valueStart) =>
      isCredentialLikeName(match[3]!) && text[valueStart] !== "-" ? "value" : null,
    ),
  (text) =>
    [...text.matchAll(standaloneSchemePattern)]
      .filter((match) => tokenShapedPattern.test(match[3]!))
      .map((match) => [match.indices![3]![0], match.indices![3]![1]] as const),
  (text) => matchSpans(text, tokenShapesPattern, 0),
];

function readTextUnits(text: string): TextUnit[] {
  const units: TextUnit[] = [];
  let index = 0;
  while (index < text.length) {
    escapeSequencePattern.lastIndex = index;
    const escape = escapeSequencePattern.exec(text);
    if (escape !== null && escape.index === index) {
      const code = Number.parseInt(escape[1] ?? escape[2] ?? escape[3]!, 16);
      // A lone surrogate reads as written, so the text the rules read stays valid Unicode.
      if (code < 0xd800 || code > 0xdfff) {
        units.push({ text: String.fromCharCode(code), start: index, end: index + escape[0].length, isMarker: false });
        index += escape[0].length;
        continue;
      }
    }
    units.push({ text: text[index]!, start: index, end: index + 1, isMarker: false });
    index += 1;
  }
  return units;
}

/** Replaces the units each span touches with one marker unit that covers their original text. */
function replaceWithMarkers(units: readonly TextUnit[], secrets: readonly SecretSpan[]): TextUnit[] {
  const replaced: TextUnit[] = [];
  let offset = 0;
  let secretIndex = 0;
  let marker: { start: number; end: number } | null = null;
  for (const unit of units) {
    const unitStart = offset;
    const unitEnd = offset + unit.text.length;
    offset = unitEnd;
    while (secretIndex < secrets.length && secrets[secretIndex]![1] <= unitStart) {
      secretIndex += 1;
    }
    const secret = secrets[secretIndex];
    const isSecret = secret !== undefined && secret[0] < unitEnd && secret[1] > unitStart;
    if (isSecret) {
      marker = marker === null ? { start: unit.start, end: unit.end } : { start: marker.start, end: unit.end };
      if (secret[1] <= unitEnd) {
        replaced.push({ text: redactedArgumentValue, start: marker.start, end: marker.end, isMarker: true });
        marker = null;
        secretIndex += 1;
      }
      continue;
    }
    if (marker !== null) {
      replaced.push({ text: redactedArgumentValue, start: marker.start, end: marker.end, isMarker: true });
      marker = null;
    }
    replaced.push(unit);
  }
  if (marker !== null) {
    replaced.push({ text: redactedArgumentValue, start: marker.start, end: marker.end, isMarker: true });
  }
  return replaced;
}

/** The span of the given group of each match. */
function matchSpans(text: string, pattern: RegExp, group: number): SecretSpan[] {
  return [...text.matchAll(pattern)].map((match) => [match.indices![group]![0], match.indices![group]![1]] as const);
}

type ValueKind = "value" | "header" | "user";

/** The credential span of the value after each match the kind function accepts, scanning left to right. */
function findValueSecrets(
  text: string,
  pattern: RegExp,
  kindOf: (match: RegExpExecArray, valueStart: number) => ValueKind | null,
): SecretSpan[] {
  const secrets: SecretSpan[] = [];
  let position = 0;
  for (;;) {
    pattern.lastIndex = position;
    const match = pattern.exec(text);
    if (match === null) {
      return secrets;
    }
    const valueStart = match.index + match[0].length;
    const kind = kindOf(match, valueStart);
    if (kind === null) {
      position = valueStart;
      continue;
    }
    const isNamed = pattern === namedValuePattern;
    // A quote right before a name, with none after it, is the quote the value ends at.
    const outerQuote = isNamed && match[1] !== "" && match[3] === "" ? match[1]! : "";
    const value = readValue(text, valueStart, kind, outerQuote, isNamed && match[5] === ":" ? match.index : -1);
    if (value.secret !== null) {
      secrets.push(value.secret);
    }
    position = Math.max(value.end, valueStart);
  }
}

/**
 * Reads the value that starts at `start`: a triple-quoted string, a YAML block scalar after `name:` (when
 * `fieldStart` is the field's index), a header line with its folded and continued lines, or a shell word with its
 * escapes, adjacent quoted parts, and line continuations. An unclosed quote runs to the end of its line, and an
 * unclosed triple quote to the end of the text.
 * @returns Where the value ends and the span that holds the credential, or `null` for none.
 */
function readValue(
  text: string,
  start: number,
  kind: ValueKind,
  outerQuote: string,
  fieldStart: number,
): { readonly end: number; readonly secret: SecretSpan | null } {
  for (const delimiter of ['"""', "'''"]) {
    if (text.startsWith(delimiter, start)) {
      const close = text.indexOf(delimiter, start + 3);
      const end = close < 0 ? text.length : close + 3;
      return { end, secret: secretSpan(text, start + 3, close < 0 ? text.length : close) };
    }
  }
  const lineEnd = findLineEnd(text, start);
  if (fieldStart >= 0 && yamlBlockIndicatorPattern.test(text.slice(start, lineEnd))) {
    const blockEnd = findYamlBlockEnd(text, fieldStart, lineEnd);
    return { end: blockEnd, secret: blockEnd > lineEnd ? secretSpan(text, lineEnd + 1, blockEnd) : null };
  }
  if (kind === "header" && !isQuote(text[start])) {
    const end = findHeaderEnd(text, start, outerQuote);
    const scheme = authorizationSchemePattern.exec(text.slice(start, end));
    return { end, secret: secretSpan(text, start + (scheme === null ? 0 : scheme[0].length), end) };
  }
  const end = findShellWordEnd(text, start, outerQuote);
  const quote = text[start];
  const isQuoted = isQuote(quote) && end - start >= 2 && text[end - 1] === quote;
  let secretStart = isQuoted ? start + 1 : start;
  const secretEnd = isQuoted ? end - 1 : end;
  if (kind === "user") {
    const separator = text.slice(secretStart, secretEnd).indexOf(":");
    secretStart = separator < 0 ? secretEnd : secretStart + separator + 1;
  }
  return { end, secret: secretSpan(text, secretStart, secretEnd) };
}

/** The span, or `null` when it is empty or already exactly the marker. */
function secretSpan(text: string, start: number, end: number): SecretSpan | null {
  const secret = text.slice(start, end);
  return secret === "" || secret === redactedArgumentValue ? null : [start, end];
}

function findLineEnd(text: string, start: number): number {
  const newline = text.indexOf("\n", start);
  return newline < 0 ? text.length : newline;
}

/** The end of the YAML block whose indicator line ends at `indicatorEnd`: its lines indented past the field's. */
function findYamlBlockEnd(text: string, fieldStart: number, indicatorEnd: number): number {
  const fieldLineStart = text.lastIndexOf("\n", fieldStart - 1) + 1;
  const fieldIndent = countIndent(text, fieldLineStart);
  let blockEnd = indicatorEnd;
  let lineStart = indicatorEnd + 1;
  while (lineStart <= text.length && indicatorEnd < text.length) {
    const lineEnd = findLineEnd(text, lineStart);
    const isBlank = text.slice(lineStart, lineEnd).trim() === "";
    if (!isBlank && countIndent(text, lineStart) <= fieldIndent) {
      break;
    }
    if (!isBlank) {
      blockEnd = lineEnd;
    }
    if (lineEnd >= text.length) {
      break;
    }
    lineStart = lineEnd + 1;
  }
  return blockEnd;
}

function countIndent(text: string, lineStart: number): number {
  let index = lineStart;
  while (text[index] === " " || text[index] === "\t") {
    index += 1;
  }
  return index - lineStart;
}

/** The end of a header value: its line, and each next line a `\\` continues or that starts with a space or tab. */
function findHeaderEnd(text: string, start: number, outerQuote: string): number {
  let index = start;
  while (index < text.length && text[index] !== outerQuote) {
    if (text[index] === "\\" && (text[index + 1] === "\n" || text[index + 1] === "\r")) {
      index += text[index + 1] === "\r" && text[index + 2] === "\n" ? 3 : 2;
      continue;
    }
    if (text[index] === "\n" || text[index] === "\r") {
      const next = text[index] === "\r" && text[index + 1] === "\n" ? index + 2 : index + 1;
      if (text[next] === " " || text[next] === "\t") {
        index = next;
        continue;
      }
      break;
    }
    index += 1;
  }
  return trimEndIndex(text, start, index);
}

function trimEndIndex(text: string, start: number, end: number): number {
  let index = end;
  while (index > start && (text[index - 1] === "\r" || text[index - 1] === "\n")) {
    index -= 1;
  }
  return index;
}

/**
 * The end of a shell word, or of a JSON or YAML value: a backslash escapes the next character, a line continuation
 * included; a closed quoted part continues the word; an unclosed quote runs to the end of its line; and a stop
 * character or `outerQuote` ends it.
 */
function findShellWordEnd(text: string, start: number, outerQuote: string): number {
  let index = start;
  while (index < text.length) {
    const character = text[index]!;
    if (outerQuote !== "" && character === outerQuote) {
      break;
    }
    if (character === "\\") {
      index += 2;
      continue;
    }
    if (isQuote(character)) {
      const quotedEnd = findQuotedEnd(text, index);
      if (quotedEnd - 1 > index && text[quotedEnd - 1] === character) {
        index = quotedEnd;
        continue;
      }
      return findLineEnd(text, index);
    }
    if (bareValueStops.has(character)) {
      break;
    }
    index += 1;
  }
  return Math.min(index, text.length);
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

function isHeaderName(name: string): boolean {
  const words = name.toLowerCase().split(/[^a-z0-9]+/);
  return words.includes("authorization") || words.includes("cookie") || words.includes("cookies");
}

function isQuote(character: string | undefined): boolean {
  return character === '"' || character === "'";
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
 * of its value, and string values and the connector, connection, and operation names have their inline credentials
 * replaced (see {@link redactInlineCredentials}), as best-effort hygiene. Longer
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
  const connectorId = redactInlineCredentials(input.connectorId);
  const connectionName = redactInlineCredentials(input.connectionName);
  const head = `Calls ${connectorId} ${redactInlineCredentials(input.operation)} with connection ${connectionName}`;
  return {
    target: `${connectorId}/${connectionName}`,
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
