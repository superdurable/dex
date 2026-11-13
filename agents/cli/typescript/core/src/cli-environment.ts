// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { homedir } from "node:os";
import { delimiter, join } from "node:path";

import { readSessionLeaseTtlSeconds } from "./session-terminal-lease.js";

/**
 * The Dex gRPC port of `dexcli dev` when `DEX_AGENTS_DEX_PORT` is not set.
 *
 * @since 0.5.0
 */
export const defaultDexcliDevDexPort = 8801;
/**
 * The Dex Web port of `dexcli dev` when `DEX_AGENTS_WEB_PORT` is not set.
 *
 * @since 0.5.0
 */
export const defaultDexcliDevWebPort = 8802;
/**
 * Where `dexcli` releases are downloaded from when `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` is not set.
 *
 * @since 0.5.0
 */
export const defaultDexcliDownloadUrl = "https://github.com/superdurable/dex/releases/download";

/**
 * The settings a command reads from its environment. Only this module reads environment variables.
 *
 * | Variable | Meaning |
 * | --- | --- |
 * | `HOME` | The home directory; `~/.dex` lives under it. |
 * | `DEXCLI` | A `dexcli` executable to use instead of finding or downloading one. |
 * | `PATH` | Where a preinstalled `dexcli` is looked for. |
 * | `DEX_AGENTS_DEX_PORT`, `DEX_AGENTS_WEB_PORT` | The ports of the `dexcli dev` that `dex.server: auto` reuses or starts. Default 8801 and 8802. |
 * | `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` | The base URL of `dexcli` release downloads, such as a mirror. |
 * | `DEX_AGENTS_OPEN_BROWSER` | Set to `0` or `false` so the command never opens a web browser. Since 0.6.0. |
 * | `DEX_API_KEY`, `DEX_TOKEN_URL`, `DEX_SERVER_ID`, `DEX_CREDENTIAL`, `DEX_CREDENTIAL_FILE`, `DEX_TLS_CA_FILE`, `DEX_TLS_SERVER_NAME` | The Dex connection settings; see {@link DexConnectionVariables}. Since 0.6.2. |
 *
 * @since 0.5.0
 */
export interface CliEnvironment {
  /** The home directory. */
  readonly homeDirectory: string;
  /** `~/.dex/agents`: configuration files, the started server's log and record, and the Worker's blob cache. */
  readonly agentsDirectory: string;
  /** `~/.dex/bin`: downloaded `dexcli` releases. */
  readonly binDirectory: string;
  /** The `DEXCLI` executable, or `undefined`. */
  readonly dexcliPath: string | undefined;
  /** The `PATH` directories, in order. */
  readonly executableSearchPath: readonly string[];
  /** The Dex gRPC port of the local `dexcli dev`. */
  readonly dexcliDevDexPort: number;
  /** The Dex Web port of the local `dexcli dev`. */
  readonly dexcliDevWebPort: number;
  /** The base URL of `dexcli` release downloads. */
  readonly dexcliDownloadUrl: string;
  /**
   * Whether an interactive command may open Dex Web pages, such as the Connectors page, in a web browser.
   * `DEX_AGENTS_OPEN_BROWSER` set to `0` or `false` turns it off; any other value, or none, leaves it on.
   * {@link readCliEnvironment} always sets it. An environment built by hand may omit it, which means `true`.
   *
   * @since 0.6.0
   */
  readonly isBrowserOpeningEnabled?: boolean;
  /**
   * The Dex connection variables, which only a `dex.auth` mode or an `https` `dex.server` puts to use.
   * {@link readCliEnvironment} always sets it. An environment built by hand may omit it, which means none is set.
   *
   * @since 0.6.2
   */
  readonly dexConnectionVariables?: DexConnectionVariables;
  /**
   * How many seconds a session's terminal lease lasts unless the command renews it; the command renews it every
   * third of that. `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS`, an integer from 1 to 3600, overrides it for tests; any
   * other value is ignored. {@link readCliEnvironment} always sets it. An environment built by hand may omit it,
   * which means 15.
   *
   * @since 0.6.3
   */
  readonly sessionLeaseTtlSeconds?: number;
  /**
   * The variables that child processes, such as `dexcli dev`, inherit.
   *
   * Changed in 0.6.2: it lacks the Dex credential variables in {@link dexSecretVariableNames}.
   */
  readonly childProcessVariables: NodeJS.ProcessEnv;
}

/**
 * The variables that hold a Dex credential or point at one. Only the Worker host and `dexcli agents-tui` get them,
 * never a tool, a browser, a download, or `dexcli dev`.
 *
 * @since 0.6.2
 */
export const dexSecretVariableNames: readonly string[] = Object.freeze([
  "DEX_API_KEY",
  "DEX_CREDENTIAL",
  "DEX_CREDENTIAL_FILE",
]);

/**
 * Removes the Dex credential variables in {@link dexSecretVariableNames} from an environment.
 *
 * A command calls it on `process.env` right after {@link readCliEnvironment} kept their values, whether or not
 * `dex.auth` is set: a shell exported for `dexcli` may hold them. Shell tools, browsers, downloads, and `dexcli dev`
 * then never see a credential, and the Worker host gets only the one variable its mode needs.
 *
 * @example
 * ```ts
 * const environment = readCliEnvironment(process.env);
 * removeDexSecretVariables(process.env);
 * ```
 * @param environment - The variables to change, such as `process.env`.
 * @since 0.6.2
 */
export function removeDexSecretVariables(environment: NodeJS.ProcessEnv): void {
  for (const name of dexSecretVariableNames) {
    delete environment[name];
  }
}

/**
 * The Dex connection variables of an environment, each trimmed. A variable that is unset, or empty after trimming,
 * is absent.
 *
 * They use `dexcli`'s names, but a command reads them only when its configuration asks for them:
 * `DEX_API_KEY`, `DEX_TOKEN_URL`, and `DEX_SERVER_ID` for `dex.auth` mode `api_key`; `DEX_CREDENTIAL` for mode
 * `bearer` with source `env`; `DEX_CREDENTIAL_FILE` for source `file`; and `DEX_TLS_CA_FILE` and
 * `DEX_TLS_SERVER_NAME` for an `https` `dex.server`. `DEX_API_KEY` and `DEX_CREDENTIAL` are secrets: never print
 * or log them.
 *
 * @example
 * ```ts
 * const variables = readDexConnectionVariables(process.env);
 * const hasApiKey = variables.DEX_API_KEY !== undefined;
 * ```
 * @since 0.6.2
 */
export interface DexConnectionVariables {
  /** The API key that `dex.auth` mode `api_key` exchanges for access tokens. A secret. */
  readonly DEX_API_KEY?: string;
  /** The token endpoint, which overrides `dex.auth.token_url`. */
  readonly DEX_TOKEN_URL?: string;
  /** The server ID, which overrides `dex.auth.server_id`. */
  readonly DEX_SERVER_ID?: string;
  /** The bearer token of `dex.auth` mode `bearer` with source `env`. A secret. */
  readonly DEX_CREDENTIAL?: string;
  /** The bearer token file, which overrides `dex.auth.file`. */
  readonly DEX_CREDENTIAL_FILE?: string;
  /** The CA bundle, which overrides `dex.tls.ca_file`. */
  readonly DEX_TLS_CA_FILE?: string;
  /** The TLS server name, which overrides `dex.tls.server_name`. */
  readonly DEX_TLS_SERVER_NAME?: string;
}

const dexConnectionVariableNames = [
  "DEX_API_KEY",
  "DEX_TOKEN_URL",
  "DEX_SERVER_ID",
  "DEX_CREDENTIAL",
  "DEX_CREDENTIAL_FILE",
  "DEX_TLS_CA_FILE",
  "DEX_TLS_SERVER_NAME",
] as const;

/**
 * Reads the {@link DexConnectionVariables} of an environment: each one trimmed, and absent when it is unset or
 * empty after trimming.
 *
 * @param environment - The variables, such as `process.env`.
 * @returns The Dex connection variables.
 * @since 0.6.2
 */
export function readDexConnectionVariables(environment: NodeJS.ProcessEnv): DexConnectionVariables {
  const variables: Record<string, string> = {};
  for (const name of dexConnectionVariableNames) {
    const value = (environment[name] ?? "").trim();
    if (value !== "") {
      variables[name] = value;
    }
  }
  return variables;
}

/**
 * Reads the {@link CliEnvironment} from environment variables.
 * @param environment - The variables, such as `process.env`.
 * @returns The settings.
 * @throws {RangeError} When a port variable is not a port number.
 * @since 0.5.0
 */
export function readCliEnvironment(environment: NodeJS.ProcessEnv): CliEnvironment {
  const homeDirectory = nonEmpty(environment.HOME) ?? homedir();
  return {
    homeDirectory,
    agentsDirectory: join(homeDirectory, ".dex", "agents"),
    binDirectory: join(homeDirectory, ".dex", "bin"),
    dexcliPath: nonEmpty(environment.DEXCLI),
    executableSearchPath: (environment.PATH ?? "").split(delimiter).filter((directory) => directory !== ""),
    dexcliDevDexPort: readPort(environment, "DEX_AGENTS_DEX_PORT", defaultDexcliDevDexPort),
    dexcliDevWebPort: readPort(environment, "DEX_AGENTS_WEB_PORT", defaultDexcliDevWebPort),
    dexcliDownloadUrl: nonEmpty(environment.DEX_AGENTS_DEXCLI_DOWNLOAD_URL) ?? defaultDexcliDownloadUrl,
    isBrowserOpeningEnabled: !["0", "false"].includes((environment.DEX_AGENTS_OPEN_BROWSER ?? "").trim().toLowerCase()),
    dexConnectionVariables: readDexConnectionVariables(environment),
    sessionLeaseTtlSeconds: readSessionLeaseTtlSeconds(environment.DEX_AGENTS_SESSION_LEASE_TTL_SECONDS),
    childProcessVariables: { ...withoutDexSecretVariables(environment), HOME: homeDirectory },
  };
}

function withoutDexSecretVariables(environment: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const copy = { ...environment };
  removeDexSecretVariables(copy);
  return copy;
}

function readPort(environment: NodeJS.ProcessEnv, name: string, fallback: number): number {
  const text = nonEmpty(environment[name]);
  if (text === undefined) {
    return fallback;
  }
  const port = Number(text);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new RangeError(`${name} must be a port number from 1 to 65535, not ${text}`);
  }
  return port;
}

function nonEmpty(value: string | undefined): string | undefined {
  return value === undefined || value === "" ? undefined : value;
}
