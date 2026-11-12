// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { homedir } from "node:os";
import { delimiter, join } from "node:path";

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
  /** The variables that child processes, such as `dexcli dev`, inherit. */
  readonly childProcessVariables: NodeJS.ProcessEnv;
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
    childProcessVariables: { ...environment, HOME: homeDirectory },
  };
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
