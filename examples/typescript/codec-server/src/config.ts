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

import { readFileSync } from "node:fs";
import { parseArgs } from "node:util";

export const authModeBearerToken = "bearer-token";
export const authModeTrustedProxyHeader = "trusted-proxy-header";
export type AuthMode = typeof authModeBearerToken | typeof authModeTrustedProxyHeader;

// Spec defaults: 16 MiB + 64 KiB envelopes and 64 MiB plaintexts.
export const defaultMaxEnvelopeBytes = 16_842_752;
export const defaultMaxPlaintextBytes = 67_108_864;
// Fits one maximum envelope as base64 (about 22 MiB) plus JSON overhead.
export const defaultMaxRequestBodyBytes = 32 * 1024 * 1024;
export const defaultMaxItemsPerRequest = 1000;
// Caps gzip amplification across entries, each of which may decode to 64 MiB.
export const defaultMaxDecodedBytesPerRequest = 64 * 1024 * 1024;
export const defaultShutdownTimeoutMs = 15_000;
const minimumBearerTokenBytes = 32;

/** The decode and encode limits of protos/payload-encryption.md section 7. */
export interface PayloadSizeLimits {
  readonly maxEnvelopeBytes: number;
  readonly maxEncodedTextBytes: number;
  readonly maxDataBytes: number;
  readonly maxPlaintextBytes: number;
}

/** @returns The limits that follow from the envelope and plaintext limits. */
export function derivePayloadSizeLimits(maxEnvelopeBytes: number, maxPlaintextBytes: number): PayloadSizeLimits {
  return {
    maxEnvelopeBytes,
    maxEncodedTextBytes: 4 * Math.ceil(maxEnvelopeBytes / 3),
    maxDataBytes: maxEnvelopeBytes,
    maxPlaintextBytes,
  };
}

/** The validated configuration of one codec server process. */
export interface CodecServerConfig {
  /** Host to listen on; undefined listens on every interface. */
  readonly listenHost: string | undefined;
  readonly listenPort: number;
  /** Exactly one of keyFilePath and keysJson is set. */
  readonly keyFilePath: string | undefined;
  readonly keysJson: string | undefined;
  readonly allowedOrigins: readonly string[];
  readonly allowCredentials: boolean;
  readonly authMode: string;
  readonly bearerToken: string | undefined;
  readonly trustedIdentityHeader: string | undefined;
  readonly maxRequestBodyBytes: number;
  readonly maxItemsPerRequest: number;
  /** Bounds the total Value JSON of one /decode response. */
  readonly maxDecodedBytesPerRequest: number;
  readonly payloadSizeLimits: PayloadSizeLimits;
  readonly shutdownTimeoutMs: number;
}

/**
 * Reads flags that default to DEX_CODEC_SERVER_* environment variables.
 * Secrets never come from flags, which leak into process lists.
 * @returns The validated configuration.
 * @throws {Error} When a setting is missing, malformed, or unsafe.
 */
export function loadCodecServerConfig(
  argumentList: readonly string[],
  environment: Readonly<Record<string, string | undefined>>,
): CodecServerConfig {
  const { values: flags } = parseArgs({
    args: [...argumentList],
    strict: true,
    allowPositionals: false,
    options: {
      "listen-address": { type: "string" },
      "key-file": { type: "string" },
      "allowed-origins": { type: "string" },
      "allow-credentials": { type: "boolean" },
      "auth-mode": { type: "string" },
      "bearer-token-file": { type: "string" },
      "trusted-identity-header": { type: "string" },
      "max-request-body-bytes": { type: "string" },
      "max-items-per-request": { type: "string" },
      "max-decoded-bytes-per-request": { type: "string" },
      "max-envelope-bytes": { type: "string" },
      "max-plaintext-bytes": { type: "string" },
      "shutdown-timeout": { type: "string" },
    },
  });
  const setting = (flagName: keyof typeof flags, environmentName: string, fallback: string): string => {
    const flagValue = flags[flagName];
    if (typeof flagValue === "string" && flagValue !== "") {
      return flagValue;
    }
    const environmentValue = environment[environmentName];
    return environmentValue !== undefined && environmentValue !== "" ? environmentValue : fallback;
  };
  const integerSetting = (flagName: keyof typeof flags, environmentName: string, fallback: number): number =>
    parseIntegerSetting(environmentName, setting(flagName, environmentName, String(fallback)));

  const { host, port } = parseListenAddress(setting("listen-address", "DEX_CODEC_SERVER_LISTEN_ADDRESS", ":8080"));
  const authMode = setting("auth-mode", "DEX_CODEC_SERVER_AUTH_MODE", "");
  const keyFilePath = setting("key-file", "DEX_CODEC_SERVER_KEY_FILE", "");
  const keysJson = environment.DEX_CODEC_SERVER_KEYS_JSON ?? "";
  const trustedIdentityHeader = setting(
    "trusted-identity-header",
    "DEX_CODEC_SERVER_TRUSTED_IDENTITY_HEADER",
    "",
  ).trim();
  const config: CodecServerConfig = {
    listenHost: host,
    listenPort: port,
    keyFilePath: keyFilePath === "" ? undefined : keyFilePath,
    keysJson: keysJson === "" ? undefined : keysJson,
    allowedOrigins: parseAllowedOrigins(setting("allowed-origins", "DEX_CODEC_SERVER_ALLOWED_ORIGINS", "")),
    allowCredentials: flags["allow-credentials"] === true || parseBooleanSetting(
      "DEX_CODEC_SERVER_ALLOW_CREDENTIALS",
      environment.DEX_CODEC_SERVER_ALLOW_CREDENTIALS || "false",
    ),
    authMode,
    bearerToken: authMode === authModeBearerToken
      ? readBearerToken(
        environment.DEX_CODEC_SERVER_BEARER_TOKEN ?? "",
        setting("bearer-token-file", "DEX_CODEC_SERVER_BEARER_TOKEN_FILE", ""),
      )
      : undefined,
    trustedIdentityHeader: trustedIdentityHeader === "" ? undefined : trustedIdentityHeader,
    maxRequestBodyBytes: integerSetting(
      "max-request-body-bytes",
      "DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES",
      defaultMaxRequestBodyBytes,
    ),
    maxItemsPerRequest: integerSetting(
      "max-items-per-request",
      "DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST",
      defaultMaxItemsPerRequest,
    ),
    maxDecodedBytesPerRequest: integerSetting(
      "max-decoded-bytes-per-request",
      "DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST",
      defaultMaxDecodedBytesPerRequest,
    ),
    payloadSizeLimits: derivePayloadSizeLimits(
      integerSetting("max-envelope-bytes", "DEX_CODEC_SERVER_MAX_ENVELOPE_BYTES", defaultMaxEnvelopeBytes),
      integerSetting("max-plaintext-bytes", "DEX_CODEC_SERVER_MAX_PLAINTEXT_BYTES", defaultMaxPlaintextBytes),
    ),
    shutdownTimeoutMs: parseDurationMs(
      "DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT",
      setting("shutdown-timeout", "DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT", "15s"),
    ),
  };
  validateCodecServerConfig(config);
  return config;
}

/**
 * Rejects missing authentication, wildcard origins, and non-positive limits or timeouts.
 * @throws {Error} Naming the first problem.
 */
export function validateCodecServerConfig(config: CodecServerConfig): void {
  if ((config.keyFilePath === undefined) === (config.keysJson === undefined)) {
    throw new Error("set exactly one key source: --key-file (DEX_CODEC_SERVER_KEY_FILE) or DEX_CODEC_SERVER_KEYS_JSON");
  }
  if (config.allowedOrigins.length === 0) {
    throw new Error("set --allowed-origins (DEX_CODEC_SERVER_ALLOWED_ORIGINS) to the Dex Web origins");
  }
  for (const origin of config.allowedOrigins) {
    if (origin.includes("*")) {
      throw new Error("allowed origins must not contain wildcards");
    }
  }
  switch (config.authMode) {
    case authModeBearerToken:
      if (config.bearerToken === undefined || Buffer.byteLength(config.bearerToken) < minimumBearerTokenBytes) {
        throw new Error(`the bearer token must have at least ${minimumBearerTokenBytes} bytes`);
      }
      break;
    case authModeTrustedProxyHeader:
      if (config.trustedIdentityHeader === undefined || !/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(config.trustedIdentityHeader)) {
        throw new Error("trusted-proxy-header mode needs a valid --trusted-identity-header");
      }
      break;
    default:
      throw new Error(
        `--auth-mode must be ${authModeBearerToken} or ${authModeTrustedProxyHeader}; `
          + "the codec server never runs without authentication",
      );
  }
  const positiveLimits: Record<string, number> = {
    maxRequestBodyBytes: config.maxRequestBodyBytes,
    maxItemsPerRequest: config.maxItemsPerRequest,
    maxDecodedBytesPerRequest: config.maxDecodedBytesPerRequest,
    maxEnvelopeBytes: config.payloadSizeLimits.maxEnvelopeBytes,
    maxEncodedTextBytes: config.payloadSizeLimits.maxEncodedTextBytes,
    maxDataBytes: config.payloadSizeLimits.maxDataBytes,
    maxPlaintextBytes: config.payloadSizeLimits.maxPlaintextBytes,
    shutdownTimeoutMs: config.shutdownTimeoutMs,
  };
  for (const [name, value] of Object.entries(positiveLimits)) {
    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new Error(`${name} must be a positive integer`);
    }
  }
}

/**
 * Accepts only exact scheme://host[:port] origins and rejects "*".
 * @returns Lowercase origins, without default ports.
 */
export function parseAllowedOrigins(commaSeparated: string): string[] {
  const origins: string[] = [];
  for (const rawEntry of commaSeparated.split(",")) {
    const entry = rawEntry.trim();
    if (entry === "") {
      continue;
    }
    if (entry.includes("*")) {
      throw new Error(`allowed origin ${JSON.stringify(entry)}: wildcards are not allowed; list each Dex Web origin`);
    }
    let parsed: URL;
    try {
      parsed = new URL(entry);
    } catch {
      throw new Error(`allowed origin ${JSON.stringify(entry)} must look like https://dex-web.example.com`);
    }
    if (
      (parsed.protocol !== "https:" && parsed.protocol !== "http:")
      || parsed.host === ""
      || parsed.pathname !== "/"
      || parsed.search !== ""
      || parsed.hash !== ""
      || parsed.username !== ""
      || parsed.password !== ""
    ) {
      throw new Error(`allowed origin ${JSON.stringify(entry)} must look like https://dex-web.example.com`);
    }
    origins.push(parsed.origin.toLowerCase());
  }
  return origins;
}

function parseListenAddress(address: string): { host: string | undefined; port: number } {
  const separator = address.lastIndexOf(":");
  if (separator < 0) {
    throw new Error("listen address must look like :8080 or 127.0.0.1:8080");
  }
  const rawHost = address.slice(0, separator);
  const portText = address.slice(separator + 1);
  if (!/^[0-9]{1,5}$/.test(portText) || Number(portText) > 65535) {
    throw new Error("listen address has an invalid port");
  }
  const host = rawHost.startsWith("[") && rawHost.endsWith("]") ? rawHost.slice(1, -1) : rawHost;
  return { host: host === "" ? undefined : host, port: Number(portText) };
}

function parseIntegerSetting(name: string, text: string): number {
  if (!/^-?[0-9]+$/.test(text) || !Number.isSafeInteger(Number(text))) {
    throw new Error(`${name} is not an integer`);
  }
  return Number(text);
}

function parseBooleanSetting(name: string, text: string): boolean {
  if (text === "true" || text === "1") {
    return true;
  }
  if (text === "false" || text === "0") {
    return false;
  }
  throw new Error(`${name} is not a boolean`);
}

function parseDurationMs(name: string, text: string): number {
  const match = /^(-?[0-9]+)(ms|s|m)$/.exec(text);
  if (match === null) {
    throw new Error(`${name} is not a duration such as 15s, 500ms, or 1m`);
  }
  const unitMs = match[2] === "ms" ? 1 : match[2] === "s" ? 1000 : 60_000;
  return Number(match[1]) * unitMs;
}

function readBearerToken(environmentToken: string, tokenFilePath: string): string {
  if ((environmentToken === "") === (tokenFilePath === "")) {
    throw new Error("bearer-token mode needs exactly one of DEX_CODEC_SERVER_BEARER_TOKEN or --bearer-token-file");
  }
  if (environmentToken !== "") {
    return environmentToken;
  }
  try {
    return readFileSync(tokenFilePath, "utf8").trim();
  } catch {
    throw new Error("the bearer token file could not be read");
  }
}
