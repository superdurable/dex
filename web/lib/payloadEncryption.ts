// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** The encoding of an object Value that holds a serialized PayloadEnvelope. */
export const PAYLOAD_ENVELOPE_ENCODING = 'dex/1';

/** Every encoding that starts with this text is reserved for Dex payload formats. */
export const RESERVED_ENCODING_PREFIX = 'dex/';

/** The prefix of an encrypted error text or reason. */
export const ENCODED_TEXT_PREFIX = 'dex-encoded:';

/** The largest serialized PayloadEnvelope a reader accepts, in bytes. */
export const MAX_ENVELOPE_BYTES = 16_842_752;

/** The largest base64 text of a PayloadEnvelope, in characters: the base64 length of MAX_ENVELOPE_BYTES. */
export const MAX_ENCODED_TEXT_BYTES = 22_457_004;

/** A dex.Value in the proto3 JSON mapping, as the codec server protocol carries it. */
export type PayloadValueJSON = Record<string, unknown>;

/** A Value that this browser decrypted; it holds the plaintext Value JSON and renders with a lock badge. */
export interface DecryptedPayload {
  __dexDecryptedValue: PayloadValueJSON;
}

/** Why an encrypted Value or text is shown without its plaintext. */
export type EncryptedPayloadProblem =
  | 'not_configured'
  | 'pending'
  | 'unavailable'
  | 'unauthenticated'
  | 'forbidden'
  | 'malformed'
  | 'unsupported_marker'
  | 'unsupported_cipher'
  | 'unsupported_compression'
  | 'unknown_key'
  | 'authentication_failed'
  | 'limit_exceeded'
  | 'invalid_plaintext'
  | 'internal';

/** An encrypted Value shown as a placeholder; keyId is empty when the envelope names no valid key ID. */
export interface EncryptedPayloadPlaceholder {
  __dexEncryptedValue: { keyId: string; problem: EncryptedPayloadProblem };
}

/** The result of decoding one envelope: its plaintext Value JSON, or why it stays encrypted. */
export type PayloadDecodeOutcome = { value: PayloadValueJSON } | { problem: EncryptedPayloadProblem };

/** Returns the outcome of one envelope, or undefined while its decode has not finished. */
export type PayloadDecodeLookup = (envelope: string) => PayloadDecodeOutcome | undefined;

/**
 * The protojson names of the dex-encoded: writer fields and read points in protos/payload-encryption.md section 6:
 * WorkerErrorResponse, ServiceErrorResponse, and RecoveryErrorInfo texts, the Stop, Reset, and CloneFork reasons, and
 * FlowResult and FlowClosedHistoryEvent error messages. No other message Dex Web shows has these field names.
 */
const encodedTextFields = new Set([
  'detail',
  'stackTrace',
  'reason',
  'errorMessage',
  'originalWorkerErrorDetail',
  'originalWorkerErrorStackTrace',
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export function isDecryptedPayload(value: unknown): value is DecryptedPayload {
  return isRecord(value) && isRecord(value.__dexDecryptedValue) && Object.keys(value).length === 1;
}

export function isEncryptedPayloadPlaceholder(value: unknown): value is EncryptedPayloadPlaceholder {
  return isRecord(value) && isRecord(value.__dexEncryptedValue) && Object.keys(value).length === 1;
}

/** The key that wraps application JSON whose own keys start with "__dex", so it never reads as a marker. */
export const ESCAPED_OBJECT_KEY = '__dexEscapedObject';

/** The application object inside an escape wrapper {"__dexEscapedObject": object}, or null. */
export function escapedObjectContent(value: unknown): Record<string, unknown> | null {
  if (!isRecord(value) || Object.keys(value).length !== 1 || !isRecord(value[ESCAPED_OBJECT_KEY])) return null;
  return value[ESCAPED_OBJECT_KEY] as Record<string, unknown>;
}

/**
 * Wraps, at every depth, each object of parsed application JSON that has a key starting with "__dex", as the Dex
 * Web server does for the JSON it parses. Markers that Dex Web generates can then never be forged by data.
 */
export function escapeApplicationJSON(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(escapeApplicationJSON);
  if (!isRecord(value)) return value;
  const escaped = Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, escapeApplicationJSON(entry)]));
  return Object.keys(value).some((key) => key.startsWith('__dex')) ? { [ESCAPED_OBJECT_KEY]: escaped } : escaped;
}

/** Returns application JSON without the escape wrappers, for display and copying. */
export function unescapeApplicationJSON(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(unescapeApplicationJSON);
  if (!isRecord(value)) return value;
  const content = escapedObjectContent(value) ?? value;
  return Object.fromEntries(Object.entries(content).map(([key, entry]) => [key, unescapeApplicationJSON(entry)]));
}

/** An object Value body: { encoding, payload } with a reserved Dex encoding. */
interface ReservedObject {
  encoding: string;
  payload: string;
}

function reservedObjectOf(value: unknown): ReservedObject | null {
  if (!isRecord(value) || typeof value.encoding !== 'string') return null;
  if (!value.encoding.startsWith(RESERVED_ENCODING_PREFIX)) return null;
  if (Object.keys(value).some((key) => key !== 'encoding' && key !== 'payload')) return null;
  if (value.payload !== undefined && typeof value.payload !== 'string') return null;
  return { encoding: value.encoding, payload: typeof value.payload === 'string' ? value.payload : '' };
}

/**
 * Returns the object body of a stored Value whose encoding starts with "dex/", or null.
 *
 * The Dex Web server marks only real Values this way, as {"__dexEnvelope": {encoding, payload}}, so application
 * JSON that merely looks like an envelope is never decoded.
 */
export function reservedObjectOfStoredValue(value: unknown): ReservedObject | null {
  if (!isRecord(value) || Object.keys(value).length !== 1) return null;
  return reservedObjectOf(value.__dexEnvelope);
}

/** Whether a stored Value is a dex/1 envelope, or another Value with a reserved Dex encoding. */
export function isEncryptedStoredValue(value: unknown): boolean {
  return reservedObjectOfStoredValue(value) !== null;
}

/** Whether text uses the "dex-encoded:" format; only text that starts with the prefix does. */
export function isEncodedText(value: unknown): value is string {
  return typeof value === 'string' && value.startsWith(ENCODED_TEXT_PREFIX);
}

/** Decodes standard padded base64, or returns null when the text is not canonical base64. */
export function canonicalBase64Bytes(text: string): Uint8Array | null {
  if (text.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(text)) return null;
  let binary: string;
  try {
    binary = atob(text);
  } catch {
    return null;
  }
  if (btoa(binary) !== text) return null;
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

/** Encodes bytes as standard padded base64. */
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  for (let index = 0; index < bytes.length; index += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  }
  return btoa(binary);
}

/** Encodes text as UTF-8 and then as standard padded base64. */
export function textToBase64(text: string): string {
  return bytesToBase64(new TextEncoder().encode(text));
}

function readVarint(bytes: Uint8Array, start: number): { value: number; next: number } | null {
  let value = 0;
  let multiplier = 1;
  for (let index = start; index < bytes.length && index < start + 10; index += 1) {
    const byte = bytes[index];
    value += (byte & 0x7f) * multiplier;
    if ((byte & 0x80) === 0) return { value, next: index + 1 };
    multiplier *= 128;
  }
  return null;
}

/** Whether a key ID follows the envelope format: 1 to 64 printable ASCII bytes without spaces. */
export function isValidPayloadKeyID(keyId: string): boolean {
  return keyId.length >= 1 && keyId.length <= 64 && /^[\x21-\x7e]+$/.test(keyId);
}

/**
 * Returns the key ID that a base64 PayloadEnvelope names in clear, or an empty string.
 *
 * It reads only protobuf field 2 and checks the size before decoding, so it never decrypts or trusts the payload.
 */
export function payloadEnvelopeKeyID(envelope: string): string {
  if (envelope.length === 0 || envelope.length > MAX_ENCODED_TEXT_BYTES) return '';
  const bytes = canonicalBase64Bytes(envelope);
  if (!bytes) return '';
  let offset = 0;
  while (offset < bytes.length) {
    const tag = readVarint(bytes, offset);
    if (!tag) return '';
    offset = tag.next;
    const fieldNumber = Math.floor(tag.value / 8);
    const wireType = tag.value % 8;
    if (wireType === 0) {
      const skipped = readVarint(bytes, offset);
      if (!skipped) return '';
      offset = skipped.next;
    } else if (wireType === 1) {
      offset += 8;
    } else if (wireType === 5) {
      offset += 4;
    } else if (wireType === 2) {
      const length = readVarint(bytes, offset);
      if (!length || length.next + length.value > bytes.length) return '';
      if (fieldNumber === 2) {
        let keyId = '';
        for (const byte of bytes.subarray(length.next, length.next + length.value)) keyId += String.fromCharCode(byte);
        return isValidPayloadKeyID(keyId) ? keyId : '';
      }
      offset = length.next + length.value;
    } else {
      return '';
    }
  }
  return '';
}

/** The base64 envelope of an encoded text, or why the text after the prefix breaks the format. */
function encodedTextEnvelope(text: string): { envelope: string } | { problem: 'malformed' | 'limit_exceeded' } {
  const envelope = text.slice(ENCODED_TEXT_PREFIX.length);
  if (envelope.length > MAX_ENCODED_TEXT_BYTES) return { problem: 'limit_exceeded' };
  if (envelope.length === 0 || !canonicalBase64Bytes(envelope)) return { problem: 'malformed' };
  return { envelope };
}

function storedValueEnvelope(object: ReservedObject): string | null {
  if (object.encoding !== PAYLOAD_ENVELOPE_ENCODING) return null;
  if (object.payload.length === 0 || object.payload.length > MAX_ENCODED_TEXT_BYTES) return null;
  return object.payload;
}

/** Values loaded from blobs: application data, where no field is a protocol error text. */
const applicationContent = new WeakSet<object>();

/** Records a value loaded from a blob as application data, so its fields are never read as error texts. */
export function markApplicationContent(value: unknown) {
  if (value !== null && typeof value === 'object') applicationContent.add(value);
}

/** Options of a tree walk. */
export interface PayloadTreeOptions {
  /** False when the whole tree is one application value, such as a Stream message; only Values decode then. */
  textReadPoints?: boolean;
}

interface WalkPosition {
  parentKey?: string;
  parentOwner?: Record<string, unknown>;
  scanTexts: boolean;
}

/** CloseDecisionType FORCE_FAIL, whose closeInput string is the error text. */
const closeDecisionTypeForceFail = 4;

function isForceFailDecision(owner: Record<string, unknown> | undefined): boolean {
  return owner?.closeDecisionType === closeDecisionTypeForceFail
    || owner?.closeDecisionType === 'CLOSE_DECISION_TYPE_FORCE_FAIL';
}

/** Whether a string is a protocol error text or reason that the dex-encoded: format may encrypt. */
function isTextReadPoint(owner: Record<string, unknown>, key: string, entry: unknown, position: WalkPosition): entry is string {
  if (!position.scanTexts || !isEncodedText(entry)) return false;
  if (encodedTextFields.has(key)) return true;
  // ForceFail sends its error text as the closeInput string Value, which blob loading may unwrap.
  if (key === 'closeInput') return isForceFailDecision(owner);
  return key === 'stringValue' && position.parentKey === 'closeInput' && isForceFailDecision(position.parentOwner);
}

function childPosition(owner: Record<string, unknown>, key: string, entry: unknown, position: WalkPosition): WalkPosition {
  // An Attribute or local value, and a value loaded from a blob, hold application data.
  const isApplicationValue = (key === 'value' && 'key' in owner)
    || (entry !== null && typeof entry === 'object' && applicationContent.has(entry));
  return { parentKey: key, parentOwner: owner, scanTexts: position.scanTexts && !isApplicationValue };
}

interface PayloadSlotVisitor {
  value: (object: ReservedObject) => unknown;
  text: (text: string) => string;
}

/** Walks the Values Dex Web marked and the protocol error texts; unchanged branches keep their identity. */
function walkPayloadSlots(tree: unknown, options: PayloadTreeOptions | undefined, visitor: PayloadSlotVisitor): unknown {
  const visit = (value: unknown, position: WalkPosition): unknown => {
    const escaped = escapedObjectContent(value);
    if (escaped) {
      // Escaped application JSON: its own object is never a marker, and its fields are never protocol texts.
      let changed = false;
      const next: Record<string, unknown> = {};
      for (const [key, entry] of Object.entries(escaped)) {
        const replaced = visit(entry, { parentKey: key, parentOwner: escaped, scanTexts: false });
        if (replaced !== entry) changed = true;
        next[key] = replaced;
      }
      return changed ? { [ESCAPED_OBJECT_KEY]: next } : value;
    }
    const object = reservedObjectOfStoredValue(value);
    if (object) return visitor.value(object);
    if (Array.isArray(value)) {
      let changed = false;
      const next = value.map((entry) => {
        const replaced = visit(entry, position);
        if (replaced !== entry) changed = true;
        return replaced;
      });
      return changed ? next : value;
    }
    if (!isRecord(value) || isDecryptedPayload(value) || isEncryptedPayloadPlaceholder(value)) return value;
    let changed = false;
    const next: Record<string, unknown> = {};
    for (const [key, entry] of Object.entries(value)) {
      const replaced = isTextReadPoint(value, key, entry, position)
        ? visitor.text(entry)
        : visit(entry, childPosition(value, key, entry, position));
      if (replaced !== entry) changed = true;
      next[key] = replaced;
    }
    return changed ? next : value;
  };
  const isRootApplication = tree !== null && typeof tree === 'object' && applicationContent.has(tree);
  return visit(tree, { scanTexts: options?.textReadPoints !== false && !isRootApplication });
}

/** Returns every distinct base64 envelope in a tree: marked dex/1 Values and dex-encoded: protocol texts. */
export function collectPayloadEnvelopes(tree: unknown, options?: PayloadTreeOptions): string[] {
  const envelopes = new Set<string>();
  walkPayloadSlots(tree, options, {
    value: (object) => {
      const envelope = storedValueEnvelope(object);
      if (envelope) envelopes.add(envelope);
      return object;
    },
    text: (text) => {
      const parsed = encodedTextEnvelope(text);
      if ('envelope' in parsed) envelopes.add(parsed.envelope);
      return text;
    },
  });
  return [...envelopes];
}

/** The text shown in place of an encrypted value that this browser has not decrypted. */
export function encryptedPayloadLabel(keyId: string, problem: EncryptedPayloadProblem): string {
  if (problem === 'pending') return 'Decrypting…';
  if (keyId) return `Encrypted (key id: ${keyId})`;
  return problem === 'malformed' || problem === 'limit_exceeded' || problem === 'unsupported_marker'
    ? `Encrypted (${problem})`
    : 'Encrypted';
}

/** Explains a placeholder, for its tooltip. */
export function encryptedPayloadProblemText(problem: EncryptedPayloadProblem): string {
  switch (problem) {
    case 'not_configured':
      return 'Set a codec endpoint in Settings to decrypt this value in your browser.';
    case 'pending':
      return 'Decrypting with the codec endpoint.';
    case 'unavailable':
      return 'The codec endpoint could not be reached.';
    case 'unauthenticated':
      return 'The codec endpoint refused the request: sign in to it, or check its credentials.';
    case 'forbidden':
      return 'The codec endpoint does not allow you to decrypt values of this Flow.';
    default:
      return `Decryption failed: ${problem}.`;
  }
}

function placeholder(keyId: string, problem: EncryptedPayloadProblem): EncryptedPayloadPlaceholder {
  return { __dexEncryptedValue: { keyId, problem } };
}

function replacedStoredValue(object: ReservedObject, lookup: PayloadDecodeLookup): unknown {
  const envelope = storedValueEnvelope(object);
  const keyId = object.encoding === PAYLOAD_ENVELOPE_ENCODING ? payloadEnvelopeKeyID(object.payload) : '';
  if (!envelope) {
    return placeholder(keyId, object.encoding === PAYLOAD_ENVELOPE_ENCODING ? 'limit_exceeded' : 'unsupported_marker');
  }
  const outcome = lookup(envelope);
  if (!outcome) return placeholder(keyId, 'pending');
  if ('problem' in outcome) return placeholder(keyId, outcome.problem);
  return { __dexDecryptedValue: outcome.value } satisfies DecryptedPayload;
}

function replacedText(text: string, lookup: PayloadDecodeLookup): string {
  const parsed = encodedTextEnvelope(text);
  if ('problem' in parsed) return encryptedPayloadLabel('', parsed.problem);
  const keyId = payloadEnvelopeKeyID(parsed.envelope);
  const outcome = lookup(parsed.envelope);
  if (!outcome) return encryptedPayloadLabel(keyId, 'pending');
  if ('value' in outcome && typeof outcome.value.stringValue === 'string') return outcome.value.stringValue;
  return encryptedPayloadLabel(keyId, 'problem' in outcome ? outcome.problem : 'invalid_plaintext');
}

/**
 * Returns the tree with each encrypted Value replaced by a decrypted or placeholder marker, and each encoded text by
 * its plaintext or a placeholder text. Malformed or oversized texts become placeholders too. Unchanged branches keep
 * their identity.
 */
export function replaceEncryptedPayloads<T>(tree: T, lookup: PayloadDecodeLookup, options?: PayloadTreeOptions): T {
  return walkPayloadSlots(tree, options, {
    value: (object) => replacedStoredValue(object, lookup),
    text: (text) => replacedText(text, lookup),
  }) as T;
}

/** Whether a tree holds any encrypted Value or protocol text, including malformed ones and decrypted markers. */
export function containsEncryptedPayload(tree: unknown, options?: PayloadTreeOptions): boolean {
  let found = false;
  walkPayloadSlots(tree, options, {
    value: (object) => {
      found = true;
      return object;
    },
    text: (text) => {
      found = true;
      return text;
    },
  });
  if (found) return true;
  const visit = (value: unknown): boolean => {
    if (isDecryptedPayload(value) || isEncryptedPayloadPlaceholder(value)) return true;
    if (Array.isArray(value)) return value.some(visit);
    return isRecord(value) && Object.values(value).some(visit);
  };
  return visit(tree);
}

/**
 * The display value of an encrypted stored Value: the plaintext of a decrypted marker, or the placeholder text of a
 * marker or a raw envelope. Returns undefined for any other value.
 */
export function encryptedPayloadDisplayValue(value: unknown): unknown {
  if (isDecryptedPayload(value)) return decryptedPayloadDisplayValue(value.__dexDecryptedValue);
  if (isEncryptedPayloadPlaceholder(value)) {
    return encryptedPayloadLabel(value.__dexEncryptedValue.keyId, value.__dexEncryptedValue.problem);
  }
  const object = reservedObjectOfStoredValue(value);
  if (!object) return undefined;
  const keyId = object.encoding === PAYLOAD_ENVELOPE_ENCODING ? payloadEnvelopeKeyID(object.payload) : '';
  return encryptedPayloadLabel(keyId, 'not_configured');
}

/**
 * The display value of a decrypted Value: text, number, boolean, null, parsed JSON, or the text of a "text" object.
 * Other object encodings return { encoding, byteSize }.
 */
export function decryptedPayloadDisplayValue(value: PayloadValueJSON): unknown {
  if ('stringValue' in value) return value.stringValue;
  if ('intValue' in value) return value.intValue;
  if ('doubleValue' in value) return value.doubleValue;
  if ('boolValue' in value) return value.boolValue;
  if ('nullValue' in value) return null;
  const object = isRecord(value.objValue) ? value.objValue : {};
  const encoding = typeof object.encoding === 'string' ? object.encoding : '';
  const bytes = canonicalBase64Bytes(typeof object.payload === 'string' ? object.payload : '') ?? new Uint8Array();
  if (encoding !== 'json' && encoding !== 'text') return { encoding, byteSize: bytes.length };
  const text = new TextDecoder().decode(bytes);
  if (encoding === 'text') return text;
  try {
    return escapeApplicationJSON(JSON.parse(text));
  } catch {
    return text;
  }
}
