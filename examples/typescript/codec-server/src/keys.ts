/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import {
  FilePayloadEncryptionKeyProvider,
  type PayloadContext,
  type PayloadEncryptionKeyProvider,
} from "@superdurable/dex";

import type { CodecServerConfig } from "./config.ts";

const payloadKeyBytes = 32;
const maxKeyIdBytes = 64;

/**
 * Creates the key provider from the configured key source.
 * KMS: replace this function with a KMS-backed provider. See README.md.
 * @returns The provider for the key file or for DEX_CODEC_SERVER_KEYS_JSON.
 */
export async function createPayloadEncryptionKeyProvider(
  config: CodecServerConfig,
): Promise<PayloadEncryptionKeyProvider> {
  if (config.keyFilePath !== undefined) {
    return FilePayloadEncryptionKeyProvider.fromFile(config.keyFilePath);
  }
  return KeyFileJsonPayloadEncryptionKeyProvider.fromJson(config.keysJson ?? "");
}

/** Reads the key file format from memory, such as from an environment variable. */
export class KeyFileJsonPayloadEncryptionKeyProvider implements PayloadEncryptionKeyProvider {
  private readonly currentKey: string;
  private readonly keys: ReadonlyMap<string, Uint8Array>;

  private constructor(currentKey: string, keys: ReadonlyMap<string, Uint8Array>) {
    this.currentKey = currentKey;
    this.keys = keys;
  }

  /**
   * Parses key file JSON with the rules of protos/payload-encryption.md section 5.
   * @returns The provider.
   * @throws {Error} When the JSON is invalid. Messages name key IDs, never key bytes.
   */
  public static fromJson(keyFileJson: string): KeyFileJsonPayloadEncryptionKeyProvider {
    let parsed: unknown;
    try {
      parsed = JSON.parse(keyFileJson);
    } catch {
      throw new Error("DEX_CODEC_SERVER_KEYS_JSON is not valid key file JSON");
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      throw new Error("DEX_CODEC_SERVER_KEYS_JSON must be a JSON object");
    }
    const { current_key_id: currentKeyId, keys } = parsed as Record<string, unknown>;
    if (typeof keys !== "object" || keys === null || Array.isArray(keys)) {
      throw new Error("DEX_CODEC_SERVER_KEYS_JSON must set keys to a JSON object");
    }
    const decodedKeys = new Map<string, Uint8Array>();
    for (const [keyId, encodedKey] of Object.entries(keys)) {
      if (!isValidPayloadKeyId(keyId)) {
        throw new Error(
          `DEX_CODEC_SERVER_KEYS_JSON: key ID ${JSON.stringify(keyId)} needs 1 to 64 printable ASCII bytes`,
        );
      }
      const key = typeof encodedKey === "string" ? decodeCanonicalBase64(encodedKey) : undefined;
      if (key === undefined || key.length !== payloadKeyBytes) {
        throw new Error(`DEX_CODEC_SERVER_KEYS_JSON: key ${keyId} is not 32 bytes of standard padded base64`);
      }
      decodedKeys.set(keyId, key);
    }
    if (typeof currentKeyId !== "string" || !decodedKeys.has(currentKeyId)) {
      throw new Error("DEX_CODEC_SERVER_KEYS_JSON: current_key_id is not in keys");
    }
    return new KeyFileJsonPayloadEncryptionKeyProvider(currentKeyId, decodedKeys);
  }

  /** @returns current_key_id for every PayloadContext. */
  public async currentKeyId(_context: PayloadContext): Promise<string> {
    return this.currentKey;
  }

  /**
   * @returns A copy of the key for keyId.
   * @throws {Error} When the key ID is unknown.
   */
  public async key(_context: PayloadContext, keyId: string): Promise<Uint8Array> {
    const key = this.keys.get(keyId);
    if (key === undefined) {
      throw new Error("key ID is not in DEX_CODEC_SERVER_KEYS_JSON");
    }
    return Uint8Array.from(key);
  }
}

/** @returns Whether keyId has 1 to 64 bytes, each printable ASCII from `!` to `~`. */
export function isValidPayloadKeyId(keyId: string): boolean {
  if (keyId.length === 0 || keyId.length > maxKeyIdBytes) {
    return false;
  }
  for (let index = 0; index < keyId.length; index += 1) {
    const code = keyId.charCodeAt(index);
    if (code < 0x21 || code > 0x7e) {
      return false;
    }
  }
  return true;
}

/**
 * Decodes standard padded base64 only when re-encoding gives the identical text.
 * Buffer.from silently accepts invalid base64.
 * @returns The bytes, or undefined for non-canonical text.
 */
export function decodeCanonicalBase64(text: string): Uint8Array | undefined {
  const decoded = Buffer.from(text, "base64");
  if (decoded.toString("base64") !== text) {
    return undefined;
  }
  return new Uint8Array(decoded.buffer, decoded.byteOffset, decoded.byteLength);
}
