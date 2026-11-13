// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync } from "node:fs";
import { isIPv6 } from "node:net";

import { Ajv2020, type ErrorObject, type ValidateFunction } from "ajv/dist/2020.js";
import { parseAllDocuments } from "yaml";

/**
 * `dex.server` and `connectors.mcp` when the file omits them.
 *
 * @since 0.5.0
 */
export const defaultDexServer = "auto";
/**
 * `connectors.mcp` when the file omits it.
 *
 * @since 0.5.0
 */
export const defaultConnectorsMcp = "auto";
/**
 * `workspace` when the file omits it.
 *
 * @since 0.5.0
 */
export const defaultWorkspace = "~/dex-agent-workspace";
/**
 * `media.store` when the file omits it: the local directory `~/.dex/agents/media`.
 *
 * @since 0.5.0
 */
export const defaultMediaStore = "auto";
/**
 * `sessions.summary` when the file omits it: the session summary on for the local `dexcli dev` only.
 *
 * @since 0.6.0
 */
export const defaultSessionSummary = "auto";

const maximumPort = 65535;
const urlParts = /^https?:\/\/(?<host>\[[^\]]*\]|[^:/]+)(?::(?<port>[0-9]+))?(?:\/.*)?$/s;

/**
 * One `connectors.enabled` entry in effective form.
 *
 * @since 0.5.0
 */
export interface ConnectorEntry {
  /** Connector ID, such as `slack`. */
  readonly connector: string;
  /** Connection name, or `null` for every connection of the connector. */
  readonly connection: string | null;
  /** Snake-case operation names, or `null` for every operation the connector offers to agents. */
  readonly tools: readonly string[] | null;
}

/**
 * The local tool switches and the shell allow list of a configuration.
 *
 * @since 0.5.0
 */
export interface LocalToolsConfig {
  /** Whether the file tools are on. */
  readonly files: boolean;
  /** The shell tool. */
  readonly shell: {
    /** Whether the shell tool is on. */
    readonly enabled: boolean;
    /** Commands that run without approval, compared as whole strings. */
    readonly allow: readonly string[];
  };
  /** Whether the web fetch tool is on. */
  readonly web_fetch: boolean;
  /** Whether the browser tools are on. */
  readonly browser: boolean;
}

/**
 * The `media` section: where the images of the sessions are stored. Session settings never change it.
 *
 * @since 0.5.0
 */
export interface MediaConfig {
  /** `auto` for the local directory `~/.dex/agents/media`, or `s3://<bucket>` or `s3://<bucket>/<prefix>`. */
  readonly store: string;
  /** The URL of an S3-compatible service, or `null` for AWS S3. It is only set with an `s3://` store. */
  readonly endpoint: string | null;
}

/**
 * The `sessions` section: how the command keeps its sessions. Session settings never change it.
 *
 * @since 0.6.0
 */
export interface SessionsConfig {
  /**
   * `auto`, `on`, or `off`: whether each session keeps the indexed title, last activity time, and turn count that the
   * `sessions` subcommand shows. See {@link isSessionSummaryIndexed}.
   */
  readonly summary: "auto" | "on" | "off";
}

/**
 * The `dex.auth` section in effective form: how the Worker and the Client authenticate to the Dex server.
 *
 * Only this section turns authentication on. It never holds a secret: the API key comes from `DEX_API_KEY`, and a
 * bearer token from `DEX_CREDENTIAL` or a token file. A field the file omits is `null`, because a variable may give
 * it; `resolveDexConnectionSettings` fills it in and checks that it is there.
 *
 * - `none`: no credential.
 * - `api_key`: exchange `DEX_API_KEY` at `token_url` for access tokens scoped to `server_id`.
 * - `bearer` with `source: "env"`: the token is the value of `DEX_CREDENTIAL`.
 * - `bearer` with `source: "file"`: the token is the content of `file`, as written in the configuration file.
 *
 * @example
 * ```ts
 * const effective = resolveConfig(parseConfigYaml(text), "claude", null);
 * if (effective.dex.auth?.mode === "api_key") console.log(effective.dex.auth.token_url);
 * ```
 * @since 0.6.3
 */
export type DexAuthConfig =
  | { readonly mode: "none" }
  | { readonly mode: "api_key"; readonly token_url: string | null; readonly server_id: string | null }
  | { readonly mode: "bearer"; readonly source: "env" }
  | { readonly mode: "bearer"; readonly source: "file"; readonly file: string | null };

/**
 * The `dex.tls` section in effective form: how the Worker and the Client verify an `https` `dex.server`.
 *
 * Both fields are `null` when the file omits them. Paths keep their leading `~`.
 *
 * @since 0.6.3
 */
export interface DexTlsConfig {
  /** A PEM file of root CAs trusted in addition to the default trust store, as written, or `null`. */
  readonly ca_file: string | null;
  /** The name the server certificate must match, or `null` for the `dex.server` host. */
  readonly server_name: string | null;
}

/**
 * The effective configuration of a configuration file: every default filled in, connector entries parsed, and
 * the command's own harness section copied to `harness_options`.
 *
 * Field names are the configuration file's own snake-case names, so the value is the same JSON in every CLI
 * core. Paths keep their leading `~`; {@link expandConfigPath} expands them where they are used. A turn's
 * configuration, from {@link resolveTurn}, has the same shape.
 *
 * @since 0.5.0
 */
export interface EffectiveConfig {
  /** The model ID. */
  readonly model: string;
  /** The instructions, or `null`. */
  readonly system_prompt: string | null;
  /** The instructions file, or `null`. */
  readonly system_prompt_file: string | null;
  /**
   * The Dex server: `auto` or a URL, and how to authenticate to it and verify it. {@link resolveConfig} always sets
   * `auth` and `tls`; they are optional so that code that builds an `EffectiveConfig` itself keeps compiling, and
   * absent means `{ mode: "none" }` and no TLS settings.
   *
   * Changed in 0.6.3: adds `auth` and `tls`.
   */
  readonly dex: { readonly server: string; readonly auth?: DexAuthConfig; readonly tls?: DexTlsConfig };
  /** The connectors MCP server and the enabled connections. */
  readonly connectors: {
    /** `auto` or a loopback URL. */
    readonly mcp: string;
    /** The enabled connections, or `null` for every authorized connection. */
    readonly enabled: readonly ConnectorEntry[] | null;
  };
  /** Tools whose every call waits for approval. */
  readonly approval: { readonly always: readonly string[] };
  /** The local tools' directory, as written in the file. */
  readonly workspace: string;
  /** The local tool switches. */
  readonly local_tools: LocalToolsConfig;
  /** The media store of the images. A turn's configuration always has the file's. */
  readonly media: MediaConfig;
  /**
   * How the command keeps its sessions. A turn's configuration always has the file's. {@link resolveConfig} always
   * sets it; it is optional so that code that builds an `EffectiveConfig` itself keeps compiling, and absent means
   * `summary: "auto"`.
   *
   * @since 0.6.0
   */
  readonly sessions?: SessionsConfig;
  /** The command's own harness section, or `{}`. */
  readonly harness_options: Readonly<Record<string, unknown>>;
}

/**
 * The configuration of one turn: the file's {@link EffectiveConfig} with a session's settings applied.
 *
 * @since 0.5.0
 */
export type TurnConfig = EffectiveConfig;

/**
 * The stable error codes of a rejected configuration file or session settings, as
 * `agents/conformance/cli_config.json` defines them.
 *
 * `unsupported_reasoning_effort` rejects a session's `reasoning_effort` that the harness command does not declare
 * in `AgentHarness.supportedReasoningEfforts`, at `/reasoning_effort`.
 *
 * `insecure_token_url` rejects an http `dex.auth.token_url` whose host is not loopback, at `/dex/auth/token_url`.
 * `auth_needs_server_url` rejects a `dex.auth` mode other than `none` with `dex.server: auto`, at `/dex/auth`.
 * `tls_needs_https_server` rejects `dex.tls` settings without an `https` `dex.server`, at `/dex/tls`.
 *
 * Changed in 0.7.0: adds `unsupported_reasoning_effort`.
 *
 * Changed in 0.6.3: adds `insecure_token_url`, `auth_needs_server_url`, and `tls_needs_https_server`.
 *
 * @since 0.5.0
 */
export type ConfigErrorCode =
  | "yaml"
  | "schema"
  | "invalid_url"
  | "insecure_token_url"
  | "auth_needs_server_url"
  | "tls_needs_https_server"
  | "duplicate_tool"
  | "duplicate_connection"
  | "model_required"
  | "widens_connectors"
  | "widens_local_tools"
  | "unsupported_reasoning_effort";

/**
 * A rejected configuration file or session settings, with a stable code and the JSON Pointer of the first
 * problem.
 *
 * The message is `<code> at <path>`, such as `widens_connectors at /connectors/enabled/1`, with the schema
 * keyword for a `schema` error. An `anyOf` error whose schema has a `title` ends with `. Use <title>.`, which names
 * the values the field accepts. A turn whose session settings fail resolution fails with this message.
 *
 * Changed in 0.6.0: an `anyOf` error names the values the field accepts.
 *
 * @example
 * ```ts
 * try {
 *   resolveConfig(parseConfigYaml(text), "builtin", null);
 * } catch (error) {
 *   if (error instanceof ConfigError) console.error(`${error.code} at ${error.path}`);
 * }
 * ```
 * @since 0.5.0
 */
export class ConfigError extends Error {
  /**
   * Creates the error.
   * @param code - The stable error code.
   * @param path - JSON Pointer of the first problem; `""` is the whole value.
   * @param keyword - For a `schema` error, the JSON Schema keyword that failed, such as `anyOf`.
   * @param validForms - For an `anyOf` schema error, the failed schema's `title`, which names the values it accepts.
   * Since 0.6.0.
   */
  public constructor(
    public readonly code: ConfigErrorCode,
    public readonly path: string,
    public readonly keyword?: string,
    public readonly validForms?: string,
  ) {
    super(
      `${code} at ${JSON.stringify(path)}${keyword === undefined ? "" : ` (schema keyword ${keyword})`}${
        validForms === undefined ? "" : `. Use ${validForms}.`
      }`,
    );
    this.name = "ConfigError";
  }
}

let compiledValidators: { readonly config: ValidateFunction; readonly sessionSettings: ValidateFunction } | undefined;
let sessionSettingsPropertyNames: ReadonlySet<string> | undefined;

/**
 * Returns the value of a configuration file's YAML 1.2 text: the core schema, so only `true` and `false` are
 * booleans, and a key repeated in one mapping is an error. An empty or comments-only file returns `null`, which
 * {@link resolveConfig} reads as `{}`.
 *
 * @param text - The file's text.
 * @returns The parsed value.
 * @throws {ConfigError} `yaml` at `""` when the text is not one YAML 1.2 document with unique keys.
 * @since 0.5.0
 */
export function parseConfigYaml(text: string): unknown {
  const documents = parseAllDocuments(text, { version: "1.2", schema: "core", uniqueKeys: true, merge: false });
  if (!Array.isArray(documents)) {
    throw new ConfigError("yaml", "");
  }
  if (documents.length === 0) {
    return null;
  }
  const [document] = documents;
  if (documents.length > 1 || document === undefined || document.errors.length > 0) {
    throw new ConfigError("yaml", "");
  }
  return document.toJS({ maxAliasCount: 100 }) ?? null;
}

/**
 * Returns the effective configuration of a parsed configuration file.
 *
 * It checks, in this order, the schema, the URLs, the token URL's scheme, `dex.auth` and `dex.tls` against
 * `dex.server`, the connector entries in list order, and the model, and throws the first failure.
 *
 * @example
 * ```ts
 * const effective = resolveConfig(parseConfigYaml(text), "builtin", "openai/gpt-5-mini");
 * console.log(effective.local_tools.shell.allow);
 * ```
 * @param config - The parsed file; `null` or `undefined` is `{}`.
 * @param harnessSection - The command's harness section, such as `claude`.
 * @param harnessDefaultModel - The command's default model, or `null` when it has none.
 * @param harnessDefaultWorkspace - The workspace when the file sets none; defaults to {@link defaultWorkspace}.
 * Since 0.6.0.
 * @returns The effective configuration.
 * @throws {ConfigError} `schema`, `invalid_url`, `insecure_token_url`, `auth_needs_server_url`,
 * `tls_needs_https_server`, `duplicate_tool`, `duplicate_connection`, or `model_required`.
 *
 * Changed in 0.6.3: reads `dex.auth` and `dex.tls`, and checks them against `dex.server`.
 *
 * @since 0.5.0
 */
export function resolveConfig(
  config: unknown,
  harnessSection: string,
  harnessDefaultModel: string | null,
  harnessDefaultWorkspace: string = defaultWorkspace,
): EffectiveConfig {
  const value = config ?? {};
  throwFirstSchemaError(validators().config, value);
  const file = value as FileConfig;
  const dexServer = file.dex?.server ?? defaultDexServer;
  const connectorsMcp = file.connectors?.mcp ?? defaultConnectorsMcp;
  const fileAuth: FileDexAuth = file.dex?.auth ?? { mode: "none" };
  const fileTls: FileDexTls = file.dex?.tls ?? {};
  throwInvalidUrl(dexServer, "/dex/server");
  const tokenUrl = fileAuth.token_url;
  if (tokenUrl !== undefined) {
    throwInvalidUrl(tokenUrl, "/dex/auth/token_url");
  }
  throwInvalidUrl(connectorsMcp, "/connectors/mcp");
  const mediaEndpoint = file.media?.endpoint ?? null;
  if (mediaEndpoint !== null) {
    throwInvalidUrl(mediaEndpoint, "/media/endpoint");
  }
  if (tokenUrl !== undefined && !isSecureTokenUrl(tokenUrl)) {
    throw new ConfigError("insecure_token_url", "/dex/auth/token_url");
  }
  if (fileAuth.mode !== "none" && dexServer === defaultDexServer) {
    throw new ConfigError("auth_needs_server_url", "/dex/auth");
  }
  const hasTlsSettings = fileTls.ca_file !== undefined || fileTls.server_name !== undefined;
  if (hasTlsSettings && !dexServer.startsWith("https://")) {
    throw new ConfigError("tls_needs_https_server", "/dex/tls");
  }
  const enabled = file.connectors?.enabled;
  const entries = enabled === undefined ? null : parseConnectorEntries(enabled, null, false);
  const model = file.model ?? harnessDefaultModel;
  if (model === null) {
    throw new ConfigError("model_required", "/model");
  }
  const shell = file.local_tools?.shell;
  const harnessOptions = (value as Record<string, unknown>)[harnessSection];
  return {
    model,
    system_prompt: file.system_prompt ?? null,
    system_prompt_file: file.system_prompt_file ?? null,
    dex: {
      server: dexServer,
      auth: effectiveDexAuth(fileAuth),
      tls: { ca_file: fileTls.ca_file ?? null, server_name: fileTls.server_name ?? null },
    },
    connectors: { mcp: connectorsMcp, enabled: entries },
    approval: { always: [...(file.approval?.always ?? [])] },
    workspace: file.workspace ?? harnessDefaultWorkspace,
    local_tools: {
      files: file.local_tools?.files ?? true,
      shell: { enabled: shell?.enabled ?? true, allow: [...(shell?.allow ?? [])] },
      web_fetch: file.local_tools?.web_fetch ?? true,
      browser: file.local_tools?.browser ?? true,
    },
    media: { store: file.media?.store ?? defaultMediaStore, endpoint: mediaEndpoint },
    sessions: { summary: file.sessions?.summary ?? defaultSessionSummary },
    harness_options: harnessOptions === undefined ? {} : structuredClone(harnessOptions as Record<string, unknown>),
  };
}

/**
 * Returns whether the command's sessions keep the indexed session summary.
 *
 * `sessions.summary: auto` keeps it only with `dex.server: auto`, the `dexcli dev` of this machine that the command
 * reuses or starts. A Dex server URL may be shared, so its search index and index keys stay untouched unless
 * `sessions.summary` is `on`.
 *
 * @param config - The file's effective configuration.
 * @returns `true` to build the session Flow with `summaryAttributesIndexed`.
 * @since 0.6.0
 */
export function isSessionSummaryIndexed(config: EffectiveConfig): boolean {
  const summary = config.sessions?.summary ?? defaultSessionSummary;
  return summary === "on" || (summary === "auto" && config.dex.server === defaultDexServer);
}

/**
 * One turn's resolution by {@link resolveTurn}: its configuration, the ignored settings keys, and its reasoning
 * effort.
 *
 * @since 0.7.0
 */
export interface TurnResolution {
  /** The turn's configuration: the file's effective configuration with the session's settings applied. */
  readonly turn: TurnConfig;
  /** The JSON Pointers of the settings' ignored top-level keys, sorted by key in Unicode code point order. */
  readonly warnings: readonly string[];
  /**
   * The session's `reasoning_effort`, one of the harness command's declared values. Absent when the settings set
   * none: the model's own default applies, and the harness passes no reasoning effort to its SDK.
   */
  readonly reasoningEffort?: string;
}

/**
 * Returns one turn's configuration: the file's effective configuration with a session's settings applied, and
 * the JSON Pointers of the settings' ignored top-level keys.
 *
 * `model` and `system_prompt` replace the file's values. `connectors.enabled` must fall inside the file's
 * entries, `approval.always` adds tools, and `local_tools` may only switch tools off or shrink the shell allow
 * list. `reasoning_effort` must be one of `supportedReasoningEfforts`, and it is checked after everything else.
 * The configuration file has no reasoning effort, so the turn's configuration does not carry it; the result's
 * `reasoningEffort` does. Settings that fail are rejected as a whole. Warnings are sorted by key in Unicode code
 * point order.
 *
 * Changed in 0.7.0: takes the harness command's reasoning efforts and returns the turn's `reasoningEffort`.
 *
 * @example
 * ```ts
 * const { turn, warnings, reasoningEffort } = resolveTurn(
 *   effective,
 *   { connectors: { enabled: ["hackernews"] }, reasoning_effort: "high" },
 *   ["low", "medium", "high"],
 * );
 * ```
 * @param effective - The file's effective configuration.
 * @param sessionSettings - The session's settings, or `null` for a session without settings.
 * @param supportedReasoningEfforts - The values `reasoning_effort` may take, the harness command's
 * `AgentHarness.supportedReasoningEfforts`. Defaults to none, which rejects every `reasoning_effort`. Since 0.7.0.
 * @returns The turn's configuration, the warnings, and the reasoning effort.
 * @throws {ConfigError} `schema`, `duplicate_tool`, `duplicate_connection`, `widens_connectors`,
 * `widens_local_tools`, or `unsupported_reasoning_effort`.
 * @since 0.5.0
 */
export function resolveTurn(
  effective: EffectiveConfig,
  sessionSettings: unknown,
  supportedReasoningEfforts: readonly string[] = [],
): TurnResolution {
  if (sessionSettings === null || sessionSettings === undefined) {
    return { turn: structuredClone(effective), warnings: [] };
  }
  throwFirstSchemaError(validators().sessionSettings, sessionSettings);
  const settings = sessionSettings as SessionSettingsValue;
  const knownKeys = sessionSettingsKnownKeys();
  const warnings = Object.keys(settings)
    .filter((key) => !knownKeys.has(key))
    .sort(compareCodePoints)
    .map((key) => jsonPointerOf([key]));
  let enabled = effective.connectors.enabled;
  if (settings.connectors?.enabled !== undefined) {
    enabled = parseConnectorEntries(settings.connectors.enabled, effective.connectors.enabled, true);
  }
  const always = [...effective.approval.always];
  for (const tool of settings.approval?.always ?? []) {
    if (!always.includes(tool)) {
      always.push(tool);
    }
  }
  const turn: TurnConfig = {
    ...structuredClone(effective),
    ...(settings.model === undefined ? {} : { model: settings.model }),
    ...(settings.system_prompt === undefined
      ? {}
      : { system_prompt: settings.system_prompt, system_prompt_file: null }),
    connectors: { mcp: effective.connectors.mcp, enabled: structuredClone(enabled) },
    approval: { always },
    local_tools: narrowLocalTools(effective.local_tools, settings.local_tools ?? {}),
  };
  const reasoningEffort = settings.reasoning_effort;
  if (reasoningEffort === undefined) {
    return { turn, warnings };
  }
  if (!supportedReasoningEfforts.includes(reasoningEffort)) {
    throw new ConfigError("unsupported_reasoning_effort", "/reasoning_effort");
  }
  return { turn, warnings, reasoningEffort };
}

/**
 * Returns an operation's snake-case name by the connectors library's `lowerCamelToSnake` rule: a capital
 * starts a word after a lowercase letter or a digit, or when it ends an acronym run before a lowercase letter
 * other than a plural `s`. So `listStoryIDs` is `list_story_ids` and `getHTTPResponse` is `get_http_response`.
 *
 * @param name - A snake-case name or an operationId.
 * @returns The snake-case name.
 * @since 0.5.0
 */
export function canonicalOperationName(name: string): string {
  let converted = "";
  for (let index = 0; index < name.length; index += 1) {
    const character = name[index] ?? "";
    if (!isAsciiUpper(character)) {
      converted += character;
      continue;
    }
    const previous = name[index - 1] ?? "";
    const following = name[index + 1] ?? "";
    const afterFollowing = name[index + 2] ?? "";
    const isPreviousLowerOrDigit = isAsciiLower(previous) || (previous >= "0" && previous <= "9" && previous !== "");
    const isAcronymPlural = following === "s" && !isAsciiLower(afterFollowing);
    const isEndOfAcronym = isAsciiUpper(previous) && isAsciiLower(following) && !isAcronymPlural;
    if (isPreviousLowerOrDigit || isEndOfAcronym) {
      converted += "_";
    }
    converted += character.toLowerCase();
  }
  return converted;
}

/**
 * Returns whether a shell command runs without approval: its whole text equals an allow list entry.
 *
 * @param command - The command text.
 * @param allow - The allow list.
 * @returns `true` for an exact match.
 * @since 0.5.0
 */
export function isShellCommandAllowed(command: string, allow: readonly string[]): boolean {
  return allow.includes(command);
}

/**
 * Compares two strings by Unicode code point, unlike the default `Array.prototype.sort`, which compares UTF-16
 * code units.
 *
 * @param left - First string.
 * @param right - Second string.
 * @returns A negative number, zero, or a positive number.
 * @since 0.5.0
 */
export function compareCodePoints(left: string, right: string): number {
  const leftPoints = Array.from(left, (character) => character.codePointAt(0) ?? 0);
  const rightPoints = Array.from(right, (character) => character.codePointAt(0) ?? 0);
  const length = Math.min(leftPoints.length, rightPoints.length);
  for (let index = 0; index < length; index += 1) {
    const difference = (leftPoints[index] ?? 0) - (rightPoints[index] ?? 0);
    if (difference !== 0) {
      return difference;
    }
  }
  return leftPoints.length - rightPoints.length;
}

/**
 * Returns whether a connector entry covers a connection, and an operation when one is given.
 *
 * @param entry - The entry.
 * @param connector - Connector ID.
 * @param connection - Connection name.
 * @param operation - Snake-case operation name, or `undefined` to check the connection only.
 * @returns `true` when the entry covers it.
 * @since 0.5.0
 */
export function isCoveredByConnectorEntry(
  entry: ConnectorEntry,
  connector: string,
  connection: string,
  operation?: string,
): boolean {
  if (entry.connector !== connector || (entry.connection !== null && entry.connection !== connection)) {
    return false;
  }
  return operation === undefined || entry.tools === null || entry.tools.includes(canonicalOperationName(operation));
}

/** The file's parsed value after the schema accepted it. */
interface FileConfig {
  readonly model?: string;
  readonly system_prompt?: string;
  readonly system_prompt_file?: string;
  readonly dex?: { readonly server?: string; readonly auth?: FileDexAuth; readonly tls?: FileDexTls };
  readonly connectors?: { readonly mcp?: string; readonly enabled?: readonly RawConnectorEntry[] };
  readonly approval?: { readonly always?: readonly string[] };
  readonly workspace?: string;
  readonly local_tools?: SessionLocalTools;
  readonly media?: { readonly store?: string; readonly endpoint?: string };
  readonly sessions?: { readonly summary?: "auto" | "on" | "off" };
}

/** `dex.auth` after the schema accepted it. */
interface FileDexAuth {
  readonly mode: "none" | "api_key" | "bearer";
  readonly token_url?: string;
  readonly server_id?: string;
  readonly source?: "env" | "file";
  readonly file?: string;
}

/** `dex.tls` after the schema accepted it. */
interface FileDexTls {
  readonly ca_file?: string;
  readonly server_name?: string;
}

/** Session settings after the schema accepted them. */
interface SessionSettingsValue {
  readonly model?: string;
  readonly reasoning_effort?: string;
  readonly system_prompt?: string;
  readonly connectors?: { readonly enabled?: readonly RawConnectorEntry[] };
  readonly approval?: { readonly always?: readonly string[] };
  readonly local_tools?: SessionLocalTools;
}

interface SessionLocalTools {
  readonly files?: boolean;
  readonly shell?: { readonly enabled?: boolean; readonly allow?: readonly string[] };
  readonly web_fetch?: boolean;
  readonly browser?: boolean;
}

type RawConnectorEntry = string | Readonly<Record<string, { readonly tools: readonly string[] }>>;

function validators(): { readonly config: ValidateFunction; readonly sessionSettings: ValidateFunction } {
  if (compiledValidators === undefined) {
    // verbose gives each error its parentSchema, whose title names the forms an anyOf accepts.
    const ajv = new Ajv2020({ strict: true, allErrors: false, verbose: true });
    compiledValidators = {
      config: ajv.compile(readSchema("config.schema.json")),
      sessionSettings: ajv.compile(readSchema("session-settings.schema.json")),
    };
  }
  return compiledValidators;
}

function sessionSettingsKnownKeys(): ReadonlySet<string> {
  sessionSettingsPropertyNames ??= new Set(
    Object.keys((readSchema("session-settings.schema.json") as { properties: Record<string, unknown> }).properties),
  );
  return sessionSettingsPropertyNames;
}

/**
 * Reads one of the shared schemas that the build copies into `dist/schemas`.
 * @param name - The schema file name.
 * @returns The parsed schema.
 *
 * @since 0.5.0
 */
export function readSchema(name: string): Record<string, unknown> {
  return JSON.parse(readFileSync(new URL(`../schemas/${name}`, import.meta.url), "utf8")) as Record<string, unknown>;
}

/** Throws the outermost failure; Ajv lists a combinator's branch errors before its own. */
function throwFirstSchemaError(validate: ValidateFunction, value: unknown): void {
  if (validate(value)) {
    return;
  }
  const errors: readonly ErrorObject[] = validate.errors ?? [];
  const outermost = errors.at(-1);
  if (outermost === undefined) {
    throw new ConfigError("schema", "");
  }
  const title: unknown = (outermost.parentSchema as { readonly title?: unknown } | undefined)?.title;
  const validForms = outermost.keyword === "anyOf" && typeof title === "string" ? title : undefined;
  throw new ConfigError("schema", outermost.instancePath, outermost.keyword, validForms);
}

/** Returns `dex.auth` in effective form: the mode and its own fields, `null` when omitted. */
function effectiveDexAuth(auth: FileDexAuth): DexAuthConfig {
  if (auth.mode === "api_key") {
    return { mode: "api_key", token_url: auth.token_url ?? null, server_id: auth.server_id ?? null };
  }
  if (auth.mode === "bearer") {
    return auth.source === "file"
      ? { mode: "bearer", source: "file", file: auth.file ?? null }
      : { mode: "bearer", source: "env" };
  }
  return { mode: "none" };
}

/**
 * Returns whether a token URL that passed the schema and the `invalid_url` checks is https, or http on a loopback
 * host: `localhost` in any case, an IPv4 address in 127.0.0.0/8, or `::1` in any spelling.
 */
export function isSecureTokenUrl(url: string): boolean {
  if (url.startsWith("https://")) {
    return true;
  }
  const host = urlParts.exec(url)?.groups?.host ?? "";
  if (host.toLowerCase() === "localhost") {
    return true;
  }
  if (host.startsWith("[")) {
    // A WHATWG parser writes every spelling of ::1 as [::1].
    return new URL(`http://${host}/`).hostname === "[::1]";
  }
  return /^[0-9.]+$/.test(host) && host.split(".")[0] === "127";
}

/** Returns whether a URL that matches a schema URL pattern passes the `invalid_url` checks. */
export function isValidUrl(url: string): boolean {
  try {
    throwInvalidUrl(url, "");
    return true;
  } catch (error: unknown) {
    if (error instanceof ConfigError) {
      return false;
    }
    throw error;
  }
}

function throwInvalidUrl(url: string, path: string): void {
  if (url === "auto") {
    return;
  }
  const parts = urlParts.exec(url)?.groups;
  if (parts === undefined) {
    throw new ConfigError("invalid_url", path);
  }
  const host = parts.host ?? "";
  if (parts.port !== undefined && Number(parts.port) > maximumPort) {
    throw new ConfigError("invalid_url", path);
  }
  if (host.startsWith("[")) {
    if (!isIPv6(host.slice(1, -1))) {
      throw new ConfigError("invalid_url", path);
    }
  } else if (/^[0-9.]+$/.test(host)) {
    // A leading zero means octal to a WHATWG parser, so only canonical decimals pass.
    if (host.split(".").some((part) => !/^(?:0|[1-9][0-9]{0,2})$/.test(part) || Number(part) > 255)) {
      throw new ConfigError("invalid_url", path);
    }
  } else if (host.split(".").some((label) => label.toLowerCase().startsWith("xn--"))) {
    throw new ConfigError("invalid_url", path);
  }
}

function parseConnectorEntries(
  entries: readonly RawConnectorEntry[],
  bounds: readonly ConnectorEntry[] | null,
  isCheckingBounds: boolean,
): ConnectorEntry[] {
  const parsed: ConnectorEntry[] = [];
  entries.forEach((rawEntry, index) => {
    const path = `/connectors/enabled/${index}`;
    const entry = parseConnectorEntry(rawEntry, path);
    if (parsed.some((earlier) => isSameConnectionCovered(earlier, entry))) {
      throw new ConfigError("duplicate_connection", path);
    }
    if (isCheckingBounds && bounds !== null && !bounds.some((bound) => covers(bound, entry))) {
      throw new ConfigError("widens_connectors", path);
    }
    parsed.push(entry);
  });
  return parsed;
}

function parseConnectorEntry(entry: RawConnectorEntry, path: string): ConnectorEntry {
  let key: string;
  let tools: string[] | null = null;
  if (typeof entry === "string") {
    key = entry;
  } else {
    const [onlyEntry] = Object.entries(entry);
    if (onlyEntry === undefined) {
      throw new ConfigError("schema", path, "minProperties");
    }
    key = onlyEntry[0];
    tools = onlyEntry[1].tools.map(canonicalOperationName);
    if (new Set(tools).size !== tools.length) {
      throw new ConfigError("duplicate_tool", path);
    }
  }
  const slash = key.indexOf("/");
  const connector = slash < 0 ? key : key.slice(0, slash);
  const connection = slash < 0 ? "" : key.slice(slash + 1);
  return { connector, connection: connection === "" ? null : connection, tools };
}

function isSameConnectionCovered(first: ConnectorEntry, second: ConnectorEntry): boolean {
  return (
    first.connector === second.connector &&
    (first.connection === null || second.connection === null || first.connection === second.connection)
  );
}

function covers(bound: ConnectorEntry, entry: ConnectorEntry): boolean {
  if (bound.connector !== entry.connector) {
    return false;
  }
  if (bound.connection !== null && bound.connection !== entry.connection) {
    return false;
  }
  if (bound.tools === null) {
    return true;
  }
  const boundTools = bound.tools;
  return entry.tools !== null && entry.tools.every((tool) => boundTools.includes(tool));
}

function narrowLocalTools(file: LocalToolsConfig, session: SessionLocalTools): LocalToolsConfig {
  const files = narrowSwitch(file.files, session.files, "/local_tools/files");
  const shellEnabled = narrowSwitch(file.shell.enabled, session.shell?.enabled, "/local_tools/shell/enabled");
  let allow = [...file.shell.allow];
  if (session.shell?.allow !== undefined) {
    session.shell.allow.forEach((command, index) => {
      if (!file.shell.allow.includes(command)) {
        throw new ConfigError("widens_local_tools", `/local_tools/shell/allow/${index}`);
      }
    });
    allow = [...session.shell.allow];
  }
  const webFetch = narrowSwitch(file.web_fetch, session.web_fetch, "/local_tools/web_fetch");
  const browser = narrowSwitch(file.browser, session.browser, "/local_tools/browser");
  return { files, shell: { enabled: shellEnabled, allow }, web_fetch: webFetch, browser };
}

function narrowSwitch(fileValue: boolean, sessionValue: boolean | undefined, path: string): boolean {
  if (sessionValue === undefined) {
    return fileValue;
  }
  if (sessionValue && !fileValue) {
    throw new ConfigError("widens_local_tools", path);
  }
  return sessionValue;
}

function isAsciiUpper(character: string): boolean {
  return character !== "" && character >= "A" && character <= "Z";
}

function isAsciiLower(character: string): boolean {
  return character !== "" && character >= "a" && character <= "z";
}

function jsonPointerOf(parts: readonly string[]): string {
  return parts.map((part) => `/${part.replaceAll("~", "~0").replaceAll("/", "~1")}`).join("");
}
