// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { webURL } from './basePath';
import { DexAPIError } from './http';
import {
  canonicalBase64Bytes,
  collectPayloadEnvelopes,
  MAX_ENCODED_TEXT_BYTES,
  containsEncryptedPayload,
  ENCODED_TEXT_PREFIX,
  PAYLOAD_ENVELOPE_ENCODING,
  replaceEncryptedPayloads,
  type EncryptedPayloadProblem,
  type PayloadDecodeLookup,
  type PayloadDecodeOutcome,
  type PayloadTreeOptions,
  type PayloadValueJSON,
} from './payloadEncryption';

/**
 * The local storage key of the viewer's codec endpoint choice. It never holds a token or a payload; a Custom mode
 * bearer token is kept under CODEC_BEARER_TOKEN_STORAGE_KEY instead.
 */
export const CODEC_SETTINGS_STORAGE_KEY = 'dex-web-codec-endpoint';

/**
 * The storage key of the Custom mode bearer token and its endpoint URL. It is in local storage when
 * rememberBearerToken is set, and in the tab's session storage otherwise; never in both.
 */
export const CODEC_BEARER_TOKEN_STORAGE_KEY = 'dex-web-codec-bearer-token';

/** auto uses the local dev codec server when Dex Web reports one; custom uses the saved URL; off decrypts nothing. */
export type CodecEndpointMode = 'auto' | 'custom' | 'off';

/** The viewer's codec endpoint choice, saved in this browser only. */
export interface CodecEndpointPreference {
  mode: CodecEndpointMode;
  endpoint: string;
  /** Sends cookies, such as an SSO proxy session, with codec requests. */
  includeCredentials: boolean;
  /** Keeps the Custom mode bearer token in local storage, shared by tabs; otherwise in this tab's session storage. */
  rememberBearerToken: boolean;
}

/** What GET /api/codec-server reports about a local codec server, such as the one dexcli dev starts. */
export interface LocalCodecServer {
  configured: boolean;
  endpoint: string;
  token: string;
}

/** The codec endpoint the browser calls, after the viewer's choice and the local dev server are combined. */
export interface CodecEndpoint {
  /** The base URL without a trailing slash; requests go to /encode and /decode under it. */
  endpoint: string;
  includeCredentials: boolean;
  /** The local dev codec server's token, or the Custom mode token; sent only to this endpoint. Empty sends none. */
  bearerToken: string;
  source: 'local' | 'custom';
}

/** The Flow whose values a codec request encodes or decodes; flowType is empty when unknown. */
export interface PayloadFlowContext {
  flowId: string;
  flowType: string;
}

export const DEFAULT_CODEC_ENDPOINT_PREFERENCE: CodecEndpointPreference = {
  mode: 'auto',
  endpoint: '',
  includeCredentials: false,
  rememberBearerToken: true,
};

const maxEntriesPerRequest = 500;
const maxBase64CharactersPerRequest = 24 * 1024 * 1024;
// A hung codec server must not leave values decrypting forever.
const codecRequestTimeoutMilliseconds = 30_000;

/** Reads the saved choice; a missing or unreadable entry returns the default. */
export function readCodecEndpointPreference(storage: Pick<Storage, 'getItem'> | undefined): CodecEndpointPreference {
  try {
    const saved = JSON.parse(storage?.getItem(CODEC_SETTINGS_STORAGE_KEY) ?? 'null') as Partial<CodecEndpointPreference> | null;
    if (!saved || typeof saved !== 'object') return DEFAULT_CODEC_ENDPOINT_PREFERENCE;
    const mode = saved.mode === 'custom' || saved.mode === 'off' ? saved.mode : 'auto';
    const endpoint = typeof saved.endpoint === 'string' ? saved.endpoint : '';
    return {
      mode,
      endpoint,
      // Without a saved Custom URL there is no cookie choice yet; a new Custom configuration starts without cookies.
      includeCredentials: endpoint !== '' && saved.includeCredentials !== false,
      rememberBearerToken: saved.rememberBearerToken !== false,
    };
  } catch {
    return DEFAULT_CODEC_ENDPOINT_PREFERENCE;
  }
}

export function saveCodecEndpointPreference(storage: Pick<Storage, 'setItem'> | undefined, preference: CodecEndpointPreference) {
  try {
    storage?.setItem(CODEC_SETTINGS_STORAGE_KEY, JSON.stringify({
      mode: preference.mode,
      endpoint: preference.endpoint,
      includeCredentials: preference.includeCredentials,
      rememberBearerToken: preference.rememberBearerToken,
    }));
  } catch (storageError) {
    console.warn('Unable to save the codec endpoint preference.', storageError);
  }
}

/** The two places a Custom mode bearer token can be kept: local storage and the tab's session storage. */
export interface CodecBearerTokenStorages {
  local: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | undefined;
  session: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | undefined;
}

/** A saved Custom mode bearer token and the canonical codec endpoint URL it belongs to. */
export interface CodecBearerTokenRecord {
  endpoint: string;
  token: string;
}

function readStoredTokenRecord(storage: Pick<Storage, 'getItem'> | undefined): CodecBearerTokenRecord | null {
  try {
    const saved = JSON.parse(storage?.getItem(CODEC_BEARER_TOKEN_STORAGE_KEY) ?? 'null') as Partial<CodecBearerTokenRecord> | null;
    if (!saved || typeof saved.endpoint !== 'string' || typeof saved.token !== 'string' || !saved.token) return null;
    return { endpoint: saved.endpoint, token: saved.token };
  } catch {
    return null;
  }
}

/** Returns whether the write or removal succeeded; removing from a missing storage succeeds. */
function writeStoredTokenRecord(
  storage: Pick<Storage, 'setItem' | 'removeItem'> | undefined,
  record: CodecBearerTokenRecord | null,
): boolean {
  if (!storage) return record === null;
  try {
    if (record) storage.setItem(CODEC_BEARER_TOKEN_STORAGE_KEY, JSON.stringify({ endpoint: record.endpoint, token: record.token }));
    else storage.removeItem(CODEC_BEARER_TOKEN_STORAGE_KEY);
    return true;
  } catch {
    // The error is not logged, so it cannot carry the token.
    return false;
  }
}

/**
 * Reads the saved Custom mode bearer token record; null when none is saved or it cannot be read.
 *
 * It reads the storage that remember selects first, then the other one, so a token saved before is kept.
 */
export function readCodecBearerTokenRecord(storages: CodecBearerTokenStorages, remember: boolean): CodecBearerTokenRecord | null {
  const [preferred, other] = remember ? [storages.local, storages.session] : [storages.session, storages.local];
  return readStoredTokenRecord(preferred) ?? readStoredTokenRecord(other);
}

/**
 * Saves the record in the storage that remember selects, then removes it from the other; null removes both.
 *
 * Returns false when the record could not be written, when the other storage keeps a copy it could not remove, or
 * when null could not be removed from both storages. A failed write leaves the other storage unchanged, so a record
 * that was moving keeps its old place.
 */
export function saveCodecBearerTokenRecord(
  storages: CodecBearerTokenStorages,
  record: CodecBearerTokenRecord | null,
  remember: boolean,
): boolean {
  const [target, other] = remember ? [storages.local, storages.session] : [storages.session, storages.local];
  if (record) {
    if (!writeStoredTokenRecord(target, record)) return false;
    // A copy left in shared local storage would outlive a tab-only choice, so that counts as a failure.
    return writeStoredTokenRecord(other, null);
  }
  const removedTarget = writeStoredTokenRecord(target, null);
  const removedOther = writeStoredTokenRecord(other, null);
  return removedTarget && removedOther;
}

/** Removes the tab's session storage record; true when it is gone. */
export function removeSessionCodecBearerTokenRecord(storages: CodecBearerTokenStorages): boolean {
  return writeStoredTokenRecord(storages.session, null);
}

function isLoopbackHost(hostname: string): boolean {
  return hostname === 'localhost' || hostname === '[::1]' || /^127(?:\.\d{1,3}){3}$/.test(hostname);
}

/**
 * Returns the codec endpoint URL without a trailing slash, or an error message.
 *
 * It accepts https, and http only for loopback hosts, so decrypted values never travel in clear over a network.
 */
export function parseCodecEndpointURL(text: string): { endpoint: string } | { error: string } {
  let url: URL;
  try {
    url = new URL(text.trim());
  } catch {
    return { error: 'Enter a full URL, such as https://codec.example.com.' };
  }
  if (url.username || url.password || url.search || url.hash) {
    return { error: 'The URL must not contain credentials, a query, or a fragment.' };
  }
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && isLoopbackHost(url.hostname))) {
    return { error: 'Use https, or http only for localhost.' };
  }
  return { endpoint: `${url.origin}${url.pathname}`.replace(/\/+$/, '') };
}

/**
 * Combines the viewer's choice with the local dev codec server; null means no codec is configured.
 *
 * savedToken applies only in custom mode, and only when its endpoint equals the Custom URL. Auto mode uses the local
 * server's own token.
 */
export function resolveCodecEndpoint(
  preference: CodecEndpointPreference,
  local: LocalCodecServer | null,
  savedToken: CodecBearerTokenRecord | null = null,
): CodecEndpoint | null {
  if (preference.mode === 'off') return null;
  if (preference.mode === 'custom') {
    const parsed = parseCodecEndpointURL(preference.endpoint);
    if ('error' in parsed) return null;
    return {
      endpoint: parsed.endpoint,
      includeCredentials: preference.includeCredentials,
      bearerToken: savedToken?.endpoint === parsed.endpoint ? savedToken.token : '',
      source: 'custom',
    };
  }
  if (!local?.configured || !local.endpoint) return null;
  const parsed = parseCodecEndpointURL(local.endpoint);
  if ('error' in parsed) return null;
  return { endpoint: parsed.endpoint, includeCredentials: false, bearerToken: local.token, source: 'local' };
}

/** Reads the local codec server that Dex Web reports; null when the request fails. */
export async function fetchLocalCodecServer(signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<LocalCodecServer | null> {
  try {
    const response = await fetcher(webURL('/api/codec-server'), { cache: 'no-store', signal });
    if (!response.ok) return null;
    const body = await response.json() as Partial<LocalCodecServer>;
    return {
      configured: body.configured === true,
      endpoint: typeof body.endpoint === 'string' ? body.endpoint : '',
      token: typeof body.token === 'string' ? body.token : '',
    };
  } catch {
    return null;
  }
}

/** A failed codec request; category follows the codec server protocol, and index names the failing entry. */
export class PayloadCodecError extends Error {
  readonly category: EncryptedPayloadProblem;
  readonly status: number;
  readonly index: number | undefined;

  constructor(
    category: EncryptedPayloadProblem,
    status: number,
    index?: number,
    codec?: Pick<CodecEndpoint, 'source' | 'bearerToken' | 'includeCredentials'> | null,
  ) {
    super(payloadCodecErrorMessage(category, codec));
    this.name = 'PayloadCodecError';
    this.category = category;
    this.status = status;
    this.index = index;
  }
}

const protocolCategories = new Set<EncryptedPayloadProblem>([
  'malformed', 'unauthenticated', 'forbidden', 'limit_exceeded', 'unsupported_marker', 'unsupported_cipher',
  'unsupported_compression', 'unknown_key', 'authentication_failed', 'invalid_plaintext', 'internal',
]);

/**
 * A fixed message per category; codec server messages are not shown, so they cannot leak into the page.
 *
 * The optional endpoint names a likely cause; the message never contains its token.
 */
export function payloadCodecErrorMessage(
  category: EncryptedPayloadProblem,
  codec?: Pick<CodecEndpoint, 'source' | 'bearerToken' | 'includeCredentials'> | null,
): string {
  switch (category) {
    case 'unavailable':
      if (codec?.source === 'custom' && codec.includeCredentials) {
        return 'The codec endpoint could not be reached. If it does not allow cookies, clear Send cookies.';
      }
      return 'The codec endpoint could not be reached.';
    case 'unauthenticated':
      if (codec?.source === 'custom' && codec.bearerToken) {
        return 'The codec endpoint refused the request (unauthenticated). The bearer token is likely wrong; enter it again.';
      }
      if (codec?.source === 'custom') {
        return 'The codec endpoint refused the request (unauthenticated). It likely needs a bearer token; enter it, '
          + 'or sign in to it.';
      }
      return 'The codec endpoint refused the request (unauthenticated). Sign in to it or check its credentials.';
    case 'forbidden':
      return 'The codec endpoint does not allow this request (forbidden).';
    case 'not_configured':
      return 'No codec endpoint is configured.';
    default:
      return `The codec endpoint failed: ${category}.`;
  }
}

async function postCodecRequest<T>(
  codec: CodecEndpoint,
  path: '/encode' | '/decode',
  body: unknown,
  fetcher: typeof fetch,
): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (codec.bearerToken) headers.Authorization = `Bearer ${codec.bearerToken}`;
  let response: Response;
  try {
    response = await fetcher(`${codec.endpoint}${path}`, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
      cache: 'no-store',
      credentials: codec.includeCredentials ? 'include' : 'omit',
      referrerPolicy: 'no-referrer',
      // A same-origin redirect would keep the Authorization header, so redirects fail instead.
      redirect: 'error',
      signal: AbortSignal.timeout(codecRequestTimeoutMilliseconds),
    });
  } catch {
    throw new PayloadCodecError('unavailable', 0, undefined, codec);
  }
  if (!response.ok) {
    let category: EncryptedPayloadProblem = response.status === 401 ? 'unauthenticated'
      : response.status === 403 ? 'forbidden' : 'internal';
    let index: number | undefined;
    try {
      const failure = (await response.json() as { error?: { category?: unknown; index?: unknown } }).error;
      // A 401 stays unauthenticated, so its hint always names the credentials.
      if (response.status !== 401 && typeof failure?.category === 'string'
        && protocolCategories.has(failure.category as EncryptedPayloadProblem)) {
        category = failure.category as EncryptedPayloadProblem;
      }
      if (typeof failure?.index === 'number' && Number.isInteger(failure.index)) index = failure.index;
    } catch {
      // A body that is not the protocol error keeps the status category.
    }
    throw new PayloadCodecError(category, response.status, index, codec);
  }
  try {
    return await response.json() as T;
  } catch {
    throw new PayloadCodecError('malformed', response.status);
  }
}

function contextBody(context: PayloadFlowContext) {
  return { flow_type: context.flowType, flow_id: context.flowId };
}

/** Splits entries into requests under the entry and size limits; one oversized entry goes alone. */
export function chunkCodecEntries<T>(entries: T[], sizeOf: (entry: T) => number): T[][] {
  const chunks: T[][] = [];
  let current: T[] = [];
  let currentSize = 0;
  for (const entry of entries) {
    const size = sizeOf(entry);
    if (current.length > 0 && (current.length >= maxEntriesPerRequest || currentSize + size > maxBase64CharactersPerRequest)) {
      chunks.push(current);
      current = [];
      currentSize = 0;
    }
    current.push(entry);
    currentSize += size;
  }
  if (current.length > 0) chunks.push(current);
  return chunks;
}

/** Encodes Values with POST /encode and returns their base64 envelopes, in order. */
export async function encodePayloadValues(
  codec: CodecEndpoint,
  context: PayloadFlowContext,
  values: PayloadValueJSON[],
  fetcher: typeof fetch = fetch,
): Promise<string[]> {
  const envelopes: string[] = [];
  for (const chunk of chunkCodecEntries(values, (value) => JSON.stringify(value).length)) {
    const result = await postCodecRequest<{ envelopes?: unknown }>(codec, '/encode', {
      context: contextBody(context), values: chunk,
    }, fetcher);
    if (!Array.isArray(result.envelopes) || result.envelopes.length !== chunk.length
      || result.envelopes.some((envelope) => typeof envelope !== 'string' || envelope === '')) {
      throw new PayloadCodecError('malformed', 200);
    }
    envelopes.push(...result.envelopes as string[]);
  }
  return envelopes;
}

/** Decodes base64 envelopes with POST /decode and returns their Value JSON, in order. */
export async function decodePayloadEnvelopes(
  codec: CodecEndpoint,
  context: PayloadFlowContext,
  envelopes: string[],
  fetcher: typeof fetch = fetch,
): Promise<PayloadValueJSON[]> {
  const result = await postCodecRequest<{ values?: unknown }>(codec, '/decode', {
    context: contextBody(context), envelopes,
  }, fetcher);
  if (!Array.isArray(result.values) || result.values.length !== envelopes.length
    || result.values.some((value) => value === null || typeof value !== 'object' || Array.isArray(value))) {
    throw new PayloadCodecError('malformed', 200);
  }
  return result.values as PayloadValueJSON[];
}

/** Encodes one Value into the dex/1 object Value JSON that a Dex Web write request passes through. */
export async function encodeStoredValue(
  codec: CodecEndpoint,
  context: PayloadFlowContext,
  value: PayloadValueJSON,
  fetcher: typeof fetch = fetch,
): Promise<PayloadValueJSON> {
  const [envelope] = await encodePayloadValues(codec, context, [value], fetcher);
  return { objValue: { encoding: PAYLOAD_ENVELOPE_ENCODING, payload: envelope } };
}

/**
 * Returns a reason or error text ready to write.
 *
 * With a codec, it returns the "dex-encoded:" format. Without one, text that starts with the prefix gets one
 * leading space, so readers never try to decode it. Empty text stays empty.
 */
export async function encodeReasonText(
  codec: CodecEndpoint | null,
  context: PayloadFlowContext,
  text: string,
  fetcher: typeof fetch = fetch,
): Promise<string> {
  if (text === '') return '';
  if (!codec) return text.startsWith(ENCODED_TEXT_PREFIX) ? ` ${text}` : text;
  const [envelope] = await encodePayloadValues(codec, context, [{ stringValue: text }], fetcher);
  return `${ENCODED_TEXT_PREFIX}${envelope}`;
}

/**
 * Returns the message to show for a failed request: the decrypted error text when Dex Web passed an encrypted
 * gRPC status message and a codec decodes it, or the error's own message otherwise.
 */
export async function readableErrorMessage(
  error: unknown,
  codec: CodecEndpoint | null,
  context: PayloadFlowContext,
  fallback: string,
  fetcher: typeof fetch = fetch,
): Promise<string> {
  if (!(error instanceof Error)) return fallback;
  if (!(error instanceof DexAPIError) || !error.encodedMessage || !codec) return error.message;
  const envelope = error.encodedMessage.slice(ENCODED_TEXT_PREFIX.length);
  if (envelope.length > MAX_ENCODED_TEXT_BYTES || !canonicalBase64Bytes(envelope)) return error.message;
  try {
    const [value] = await decodePayloadEnvelopes(codec, context, [envelope], fetcher);
    return typeof value.stringValue === 'string' ? value.stringValue : error.message;
  } catch {
    return error.message;
  }
}

function contextKey(context: PayloadFlowContext): string {
  return `${context.flowType.length}:${context.flowType}${context.flowId}`;
}

interface PendingContext {
  context: PayloadFlowContext;
  envelopes: Set<string>;
}

/**
 * Decodes envelopes in batches per Flow and keeps the results in memory only.
 *
 * Requests made in the same task join one batch. A failing entry is isolated, so one bad envelope does not hide
 * the rest. Results are never written to storage or sent to Dex Web.
 */
export class PayloadDecodeStore {
  readonly codec: CodecEndpoint | null;
  private readonly fetcher: typeof fetch;
  private readonly outcomes = new Map<string, PayloadDecodeOutcome>();
  private readonly requested = new Set<string>();
  private readonly pending = new Map<string, PendingContext>();
  private readonly listeners = new Set<() => void>();
  private version = 0;
  private flushScheduled = false;
  private latestFailure: EncryptedPayloadProblem | null = null;

  constructor(codec: CodecEndpoint | null, fetcher: typeof fetch = (...args) => fetch(...args)) {
    this.codec = codec;
    this.fetcher = fetcher;
  }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  getVersion = () => this.version;

  /** The category of the latest request that failed as a whole, such as unauthenticated; null when none did. */
  failure(): EncryptedPayloadProblem | null {
    return this.latestFailure;
  }

  lookup(context: PayloadFlowContext): PayloadDecodeLookup {
    if (!this.codec) return () => ({ problem: 'not_configured' });
    const prefix = contextKey(context);
    return (envelope) => this.outcomes.get(`${prefix}\u0000${envelope}`);
  }

  request(context: PayloadFlowContext, envelopes: string[]) {
    if (!this.codec || envelopes.length === 0) return;
    const prefix = contextKey(context);
    for (const envelope of envelopes) {
      const key = `${prefix}\u0000${envelope}`;
      if (this.requested.has(key)) continue;
      this.requested.add(key);
      const entry = this.pending.get(prefix) ?? { context, envelopes: new Set<string>() };
      entry.envelopes.add(envelope);
      this.pending.set(prefix, entry);
    }
    if (this.pending.size > 0 && !this.flushScheduled) {
      this.flushScheduled = true;
      setTimeout(() => void this.flush(), 0);
    }
  }

  /** Decodes every envelope in tree and then returns the tree with plaintext or placeholders in their place. */
  async decryptTree<T>(context: PayloadFlowContext, tree: T, options?: PayloadTreeOptions): Promise<T> {
    const envelopes = collectPayloadEnvelopes(tree, options);
    const lookup = this.lookup(context);
    if (this.codec && envelopes.length > 0) {
      this.request(context, envelopes);
      await new Promise<void>((resolve) => {
        const check = () => {
          if (envelopes.every((envelope) => lookup(envelope) !== undefined)) {
            unsubscribe();
            resolve();
          }
        };
        const unsubscribe = this.subscribe(check);
        check();
      });
    }
    return containsEncryptedPayload(tree, options) ? replaceEncryptedPayloads(tree, lookup, options) : tree;
  }

  private async flush() {
    this.flushScheduled = false;
    const batches = [...this.pending.values()];
    this.pending.clear();
    await Promise.all(batches.map(async ({ context, envelopes }) => {
      for (const chunk of chunkCodecEntries([...envelopes], (envelope) => envelope.length)) {
        await this.decodeChunk(context, chunk);
      }
    }));
  }

  private async decodeChunk(context: PayloadFlowContext, envelopes: string[]) {
    const codec = this.codec;
    if (!codec) return;
    const prefix = contextKey(context);
    let remaining = envelopes;
    // Each 422 with a valid index removes one entry, so this ends within one round per entry.
    while (remaining.length > 0) {
      try {
        const values = await decodePayloadEnvelopes(codec, context, remaining, this.fetcher);
        remaining.forEach((envelope, index) => this.outcomes.set(`${prefix}\u0000${envelope}`, { value: values[index] }));
        this.latestFailure = null;
        remaining = [];
      } catch (error) {
        const failure = error instanceof PayloadCodecError ? error : new PayloadCodecError('internal', 0);
        const index = failure.index;
        const isEntryFailure = failure.status === 422 && index !== undefined && index >= 0 && index < remaining.length;
        if (!isEntryFailure) {
          remaining.forEach((envelope) => this.outcomes.set(`${prefix}\u0000${envelope}`, { problem: failure.category }));
          this.latestFailure = failure.category;
          remaining = [];
        } else {
          this.outcomes.set(`${prefix}\u0000${remaining[index]}`, { problem: failure.category });
          remaining = remaining.filter((_, position) => position !== index);
        }
      }
      this.version += 1;
      this.listeners.forEach((listener) => listener());
    }
  }
}
