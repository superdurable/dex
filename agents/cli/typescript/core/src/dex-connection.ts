// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

import { ApiKeyCredential, FileCredential, type Credential, type FlowServiceTlsOptions } from "@superdurable/dex";

import { isSecureTokenUrl, isValidUrl, readSchema, type DexAuthConfig, type EffectiveConfig } from "./cli-config.js";
import type { DexConnectionVariables } from "./cli-environment.js";

/** The gRPC status code `PERMISSION_DENIED`. */
const grpcPermissionDeniedCode = 7;
/** The gRPC status code `UNAUTHENTICATED`. */
const grpcUnauthenticatedCode = 16;
/** How the Dex SDK starts the message of a failed API key exchange. */
const tokenExchangeMessagePrefix = "dex: token exchange";

/**
 * How a command verifies the Dex server: the launch document's `dex.tls` of the terminal UI contract.
 *
 * @since 0.6.2
 */
export interface DexTlsSettings {
  /** Whether the connection uses TLS: `dex.server` is an `https` URL. */
  readonly enabled: boolean;
  /** The absolute path of a PEM file of root CAs trusted in addition to the default trust store, or `null`. */
  readonly ca_file: string | null;
  /** The name the server certificate must match, or `null` for the server address host. */
  readonly server_name: string | null;
}

/**
 * How a command authenticates to the Dex server, without the secret.
 *
 * It is the launch document's `dex.auth` of the terminal UI contract, plus the token file's absolute path in
 * `file` for `bearer` with `source: "file"`. The secret stays in the environment: `DEX_API_KEY` for `api_key`, and
 * `DEX_CREDENTIAL` for `bearer` with `source: "env"`.
 *
 * @since 0.6.2
 */
export type DexAuthSettings =
  | { readonly mode: "none" }
  | { readonly mode: "api_key"; readonly token_url: string; readonly server_id: string }
  | { readonly mode: "bearer"; readonly source: "env" }
  | { readonly mode: "bearer"; readonly source: "file"; readonly file: string };

/**
 * The Dex connection settings of a command: TLS and how to authenticate, with every variable applied and no
 * secret. Field names are snake case, so the value is the same JSON in every CLI core, as
 * `agents/conformance/cli_config.json` fixes it.
 *
 * @since 0.6.2
 */
export interface DexConnectionSettings {
  /** How to verify the server. */
  readonly tls: DexTlsSettings;
  /** How to authenticate. */
  readonly auth: DexAuthSettings;
}

/**
 * A Dex connection setting that is missing or invalid: {@link setting} names the one setting to fix, and the
 * message says how, in the same words in every CLI core. The message never holds a secret.
 *
 * @example
 * ```ts
 * try {
 *   resolveDexConnectionSettings(effective, readDexConnectionVariables(process.env), paths);
 * } catch (error) {
 *   if (error instanceof DexConnectionSettingsError) console.error(`${error.setting}: ${error.message}`);
 * }
 * ```
 * @since 0.6.2
 */
export class DexConnectionSettingsError extends Error {
  /**
   * Creates the error.
   * @param setting - The configuration field, such as `dex.auth.token_url`, or the variable, such as `DEX_API_KEY`.
   * @param message - What is wrong and how to fix it.
   */
  public constructor(
    public readonly setting: string,
    message: string,
  ) {
    super(message);
    this.name = "DexConnectionSettingsError";
  }
}

/**
 * The paths that relative file paths of the Dex connection settings resolve against.
 *
 * @since 0.6.2
 */
export interface DexConnectionPaths {
  /** The configuration file: a relative path in it is relative to its directory. */
  readonly configPath: string;
  /** The home directory, which a leading `~` stands for. */
  readonly homeDirectory: string;
  /** The working directory: a relative path in a variable is relative to it. */
  readonly workingDirectory: string;
}

/**
 * Returns the Dex connection settings of an effective configuration and its Dex connection variables.
 *
 * Only `dex.auth.mode` turns authentication on: with mode `none`, every auth variable is ignored. A variable
 * overrides the matching field of the selected mode: `DEX_TOKEN_URL`, `DEX_SERVER_ID`, and `DEX_CREDENTIAL_FILE`.
 * `DEX_TLS_CA_FILE` and `DEX_TLS_SERVER_NAME` override `dex.tls`, and apply only to an `https` `dex.server`. A path
 * from the file expands a leading `~` and is relative to the file's directory; a path from a variable expands `~`
 * and is relative to the working directory. Paths are resolved lexically; no file is read.
 *
 * For `api_key` it checks the token URL (a `DEX_TOKEN_URL` as soon as it is set), the server ID, then that
 * `DEX_API_KEY` is set. For `bearer` it checks that `DEX_CREDENTIAL`, or a token file path, is set.
 *
 * @example
 * ```ts
 * const settings = resolveDexConnectionSettings(effective, readDexConnectionVariables(process.env), {
 *   configPath: "/home/dev/.dex/agents/claude-sdk.yaml",
 *   homeDirectory: "/home/dev",
 *   workingDirectory: process.cwd(),
 * });
 * console.log(settings.auth.mode, settings.tls.enabled);
 * ```
 * @param effective - The file's effective configuration, from `resolveConfig`.
 * @param variables - The Dex connection variables, from `readDexConnectionVariables`.
 * @param paths - What relative paths resolve against.
 * @returns The settings, without a secret.
 * @throws {DexConnectionSettingsError} The first missing or invalid setting.
 * @since 0.6.2
 */
export function resolveDexConnectionSettings(
  effective: EffectiveConfig,
  variables: DexConnectionVariables,
  paths: DexConnectionPaths,
): DexConnectionSettings {
  const isTls = effective.dex.server.startsWith("https://");
  const configDirectory = dirname(paths.configPath);
  const fileTls = effective.dex.tls ?? { ca_file: null, server_name: null };
  const fromVariable = (value: string | undefined): string | null =>
    value === undefined ? null : expandPath(value, paths.workingDirectory, paths.homeDirectory);
  const fromFile = (value: string | null): string | null =>
    value === null ? null : expandPath(value, configDirectory, paths.homeDirectory);
  const tls: DexTlsSettings = isTls
    ? {
        enabled: true,
        ca_file: fromVariable(variables.DEX_TLS_CA_FILE) ?? fromFile(fileTls.ca_file),
        server_name: variables.DEX_TLS_SERVER_NAME ?? fileTls.server_name,
      }
    : { enabled: false, ca_file: null, server_name: null };
  return { tls, auth: resolveAuth(effective.dex.auth ?? { mode: "none" }, variables, fromVariable, fromFile) };
}

function resolveAuth(
  auth: DexAuthConfig,
  variables: DexConnectionVariables,
  fromVariable: (value: string | undefined) => string | null,
  fromFile: (value: string | null) => string | null,
): DexAuthSettings {
  if (auth.mode === "api_key") {
    const variableTokenUrl = variables.DEX_TOKEN_URL;
    if (variableTokenUrl !== undefined) {
      throwInvalidTokenUrlVariable(variableTokenUrl);
    }
    const tokenUrl = variableTokenUrl ?? auth.token_url;
    if (tokenUrl === null) {
      throw new DexConnectionSettingsError(
        "dex.auth.token_url",
        "dex.auth mode api_key needs a token URL; set dex.auth.token_url or DEX_TOKEN_URL.",
      );
    }
    const serverId = variables.DEX_SERVER_ID ?? auth.server_id;
    if (serverId === null) {
      throw new DexConnectionSettingsError(
        "dex.auth.server_id",
        "dex.auth mode api_key needs a server ID; set dex.auth.server_id or DEX_SERVER_ID.",
      );
    }
    if (variables.DEX_API_KEY === undefined) {
      throw new DexConnectionSettingsError("DEX_API_KEY", "DEX_API_KEY is not set; dex.auth mode api_key needs it.");
    }
    return { mode: "api_key", token_url: tokenUrl, server_id: serverId };
  }
  if (auth.mode === "bearer" && auth.source === "env") {
    if (variables.DEX_CREDENTIAL === undefined) {
      throw new DexConnectionSettingsError(
        "DEX_CREDENTIAL",
        "DEX_CREDENTIAL is not set; dex.auth mode bearer with source env needs it.",
      );
    }
    return { mode: "bearer", source: "env" };
  }
  if (auth.mode === "bearer") {
    const tokenFile = fromVariable(variables.DEX_CREDENTIAL_FILE) ?? fromFile(auth.file);
    if (tokenFile === null) {
      throw new DexConnectionSettingsError(
        "dex.auth.file",
        "dex.auth mode bearer with source file needs a token file; set dex.auth.file or DEX_CREDENTIAL_FILE.",
      );
    }
    return { mode: "bearer", source: "file", file: tokenFile };
  }
  return { mode: "none" };
}

let tokenUrlPattern: RegExp | undefined;

function throwInvalidTokenUrlVariable(tokenUrl: string): void {
  tokenUrlPattern ??= new RegExp(
    (
      readSchema("config.schema.json") as {
        properties: { dex: { properties: { auth: { properties: { token_url: { pattern: string } } } } } };
      }
    ).properties.dex.properties.auth.properties.token_url.pattern,
    "u",
  );
  if (!tokenUrlPattern.test(tokenUrl) || !isValidUrl(tokenUrl)) {
    throw new DexConnectionSettingsError(
      "DEX_TOKEN_URL",
      "DEX_TOKEN_URL must be an http or https URL without user info or a fragment.",
    );
  }
  if (!isSecureTokenUrl(tokenUrl)) {
    throw new DexConnectionSettingsError(
      "DEX_TOKEN_URL",
      "DEX_TOKEN_URL must use https; http is allowed only on localhost, 127.0.0.0/8, and [::1].",
    );
  }
}

function expandPath(path: string, baseDirectory: string, homeDirectory: string): string {
  if (path === "~" || path.startsWith("~/")) {
    return join(homeDirectory, path.slice(1));
  }
  return resolve(baseDirectory, path);
}

/** What a Client and a Worker of one command connect with. */
export interface DexConnectionOptions {
  readonly serverAddress: string;
  readonly flowServiceTls?: FlowServiceTlsOptions;
  readonly credential?: Credential;
}

/**
 * Returns the Client and Worker options of the settings: the TLS options and one credential, which the caller
 * shares between its Client and Worker. It reads the CA file and the token file once to check them.
 *
 * @throws {DexConnectionSettingsError} When the CA file or the token file cannot be read, or the token file is empty.
 */
export function createDexConnectionOptions(
  serverAddress: string,
  settings: DexConnectionSettings,
  variables: DexConnectionVariables,
): DexConnectionOptions {
  return {
    serverAddress,
    ...(settings.tls.enabled ? { flowServiceTls: flowServiceTlsOptions(settings.tls) } : {}),
    ...credentialOption(settings.auth, variables),
  };
}

function flowServiceTlsOptions(tls: DexTlsSettings): FlowServiceTlsOptions {
  let caPem: Buffer | undefined;
  if (tls.ca_file !== null) {
    try {
      caPem = readFileSync(tls.ca_file);
    } catch (error: unknown) {
      throw new DexConnectionSettingsError(
        "dex.tls.ca_file",
        `cannot read the CA file ${tls.ca_file} that dex.tls.ca_file or DEX_TLS_CA_FILE names: ${describe(error)}`,
      );
    }
  }
  return {
    ...(caPem === undefined ? {} : { additionalRootCAsPem: caPem }),
    ...(tls.server_name === null ? {} : { serverName: tls.server_name }),
  };
}

function credentialOption(
  auth: DexAuthSettings,
  variables: DexConnectionVariables,
): { readonly credential?: Credential } {
  switch (auth.mode) {
    case "api_key":
      return {
        credential: new ApiKeyCredential({
          tokenUrl: auth.token_url,
          serverId: auth.server_id,
          apiKey: requireSecret(variables.DEX_API_KEY, "DEX_API_KEY", "dex.auth mode api_key"),
        }),
      };
    case "bearer": {
      if (auth.source === "env") {
        const token = requireSecret(variables.DEX_CREDENTIAL, "DEX_CREDENTIAL", "dex.auth mode bearer with source env");
        return { credential: { token: () => token } };
      }
      return { credential: readTokenFile(auth.file) };
    }
    default:
      return {};
  }
}

function readTokenFile(path: string): FileCredential {
  let text: string;
  try {
    text = readFileSync(path, "utf8");
  } catch (error: unknown) {
    throw new DexConnectionSettingsError(
      "dex.auth.file",
      `cannot read the bearer token file ${path} that dex.auth.file or DEX_CREDENTIAL_FILE names: ${describe(error)}`,
    );
  }
  if (text.trim() === "") {
    throw new DexConnectionSettingsError(
      "dex.auth.file",
      `the bearer token file ${path} that dex.auth.file or DEX_CREDENTIAL_FILE names is empty`,
    );
  }
  return new FileCredential(path);
}

function requireSecret(value: string | undefined, name: string, mode: string): string {
  if (value === undefined) {
    throw new DexConnectionSettingsError(name, `${name} is not set; ${mode} needs it.`);
  }
  return value;
}

/**
 * Returns the error for a Client or Worker constructor that refused the TLS options, or `undefined` for another
 * failure.
 */
export function describeRefusedTlsOptions(failure: unknown, tls: DexTlsSettings): DexConnectionSettingsError | undefined {
  if (!(failure instanceof TypeError)) {
    return undefined;
  }
  if (tls.server_name !== null && failure.message.includes("serverName")) {
    return new DexConnectionSettingsError(
      "dex.tls.server_name",
      `dex.tls.server_name or DEX_TLS_SERVER_NAME is not a valid TLS server name: ${failure.message}`,
    );
  }
  if (tls.ca_file !== null && failure.message.includes("additionalRootCAsPem")) {
    return new DexConnectionSettingsError(
      "dex.tls.ca_file",
      `the CA file ${tls.ca_file} that dex.tls.ca_file or DEX_TLS_CA_FILE names holds no PEM certificate`,
    );
  }
  return undefined;
}

/**
 * Returns the one-line error for a call that the token endpoint or the Dex server refused for its credential.
 *
 * A failed API key exchange names `DEX_API_KEY`, the token URL, and the HTTP status; an `UNAUTHENTICATED` or
 * `PERMISSION_DENIED` answer names the credential the command sent and the status code. Neither holds a secret, and
 * neither repeats a remote error text, which may echo the credential.
 *
 * @param failure - The failure, whose cause chain is searched.
 * @param auth - How the command authenticates.
 * @param serverAddress - The Dex server's address.
 * @returns The error text, or `undefined` when no credential problem caused the failure.
 */
export function describeRefusedDexCredential(
  failure: unknown,
  auth: DexAuthSettings,
  serverAddress: string,
): string | undefined {
  const exchangeMessage = tokenExchangeFailure(failure);
  if (exchangeMessage !== undefined && auth.mode === "api_key") {
    const httpStatus = /HTTP status (\d{3}|unavailable)/.exec(exchangeMessage)?.[1] ?? "unavailable";
    return (
      `could not exchange DEX_API_KEY for an access token at ${auth.token_url} (HTTP status ${httpStatus}). ` +
      "Check DEX_API_KEY, dex.auth.token_url, and dex.auth.server_id."
    );
  }
  const status = grpcStatusOf(failure, [grpcUnauthenticatedCode, grpcPermissionDeniedCode]);
  if (status === undefined) {
    return undefined;
  }
  const what = describeCredential(auth);
  if (status.code === grpcPermissionDeniedCode) {
    return (
      `the Dex server at ${serverAddress} denied ${what} (PERMISSION_DENIED). ` +
      "Use a credential that may run Workers and start Flows."
    );
  }
  return `the Dex server at ${serverAddress} rejected ${what} (UNAUTHENTICATED). ${credentialHint(auth)}`;
}

function describeCredential(auth: DexAuthSettings): string {
  switch (auth.mode) {
    case "api_key":
      return "the access token for DEX_API_KEY";
    case "bearer":
      return auth.source === "env" ? "the bearer token in DEX_CREDENTIAL" : `the bearer token in ${auth.file}`;
    default:
      return "the request without a credential";
  }
}

function credentialHint(auth: DexAuthSettings): string {
  switch (auth.mode) {
    case "api_key":
      return "Check DEX_API_KEY and dex.auth.server_id.";
    case "bearer":
      return auth.source === "env" ? "Check DEX_CREDENTIAL." : "Check that file, or DEX_CREDENTIAL_FILE.";
    default:
      return "Set dex.auth in the configuration file.";
  }
}

/** Returns the message of a failed API key exchange in the cause chain, or `undefined`. */
function tokenExchangeFailure(failure: unknown): string | undefined {
  for (const link of causeChain(failure)) {
    if (link instanceof Error && link.name === "ApiKeyExchangeError") {
      return link.message;
    }
    // A credential interceptor reports the exchange as UNAUTHENTICATED with the exchange message as details.
    const details = statusDetails(link);
    if (details?.startsWith(tokenExchangeMessagePrefix) === true) {
      return details;
    }
  }
  return undefined;
}

/** Returns the first gRPC status code of the codes in the cause chain, matched by shape. */
function grpcStatusOf(failure: unknown, codes: readonly number[]): { readonly code: number } | undefined {
  for (const link of causeChain(failure)) {
    const code = (link as { readonly code?: unknown }).code;
    if (typeof code === "number" && codes.includes(code)) {
      return { code };
    }
  }
  return undefined;
}

function statusDetails(link: object): string | undefined {
  // A grpc-js ServiceError has details; the SDK's DexServiceError has detail.
  const candidate = link as { readonly details?: unknown; readonly detail?: unknown };
  for (const value of [candidate.details, candidate.detail]) {
    if (typeof value === "string" && value !== "") {
      return value;
    }
  }
  return undefined;
}

function causeChain(failure: unknown): object[] {
  const chain: object[] = [];
  let current: unknown = failure;
  while (typeof current === "object" && current !== null && !chain.includes(current)) {
    chain.push(current);
    current = (current as { readonly cause?: unknown }).cause;
  }
  return chain;
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
