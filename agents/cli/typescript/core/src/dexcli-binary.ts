// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { accessSync, constants, existsSync, lstatSync, statSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";

import type { CliEnvironment } from "./cli-environment.js";
import { readWantedTarFiles } from "./release-archive.js";

/**
 * The `dexcli` release a command downloads when it finds no `dexcli` of at least this version.
 *
 * @since 0.5.0
 */
export const pinnedDexcliVersion = "1.45.0";

const downloadTimeoutMs = 300_000;
const maximumRedirects = 5;
const execFileAsync = promisify(execFile);
const installedExecutables = ["dexcli", "dex-server-zd"] as const;
const installRecordName = "install.json";

/** `DEXCLI` names no executable file; the command cannot fall back from it. */
export class DexcliVariableError extends Error {}

/**
 * Returns a `dexcli` executable, downloading the pinned release on first use.
 *
 * The order is: `DEXCLI`; `~/.dex/bin/dexcli-<version>/dexcli` from an earlier download, after its files match the
 * SHA-256 digests recorded when it was installed; the first `dexcli` on `PATH` whose version is at least
 * {@link pinnedDexcliVersion}; otherwise the pinned release for this platform, downloaded from
 * `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` (GitHub releases by default), which must be `https`, or `http` on a loopback
 * host for development, as must every redirect. The archive must match the release's
 * `checksums.txt` SHA-256. Only its regular `dexcli` and `dex-server-zd` files are written, into a private
 * staging directory that is renamed into place; an archive with a link, an absolute path, or a `..` entry is
 * refused.
 *
 * @param environment - The command's environment settings.
 * @param log - Writes progress for the person at the terminal.
 * @returns The executable's path; `dex-server-zd` sits next to it unless it came from `PATH`.
 * @throws {Error} When `DEXCLI` is not executable, a cached file changed since it was installed, the platform has
 * no release, or the download, checksum, or archive check fails.
 * @since 0.5.0
 */
export async function resolveDexcliExecutable(
  environment: CliEnvironment,
  log: (line: string) => void,
): Promise<string> {
  if (environment.dexcliPath !== undefined) {
    if (!isExecutable(environment.dexcliPath)) {
      throw new DexcliVariableError(
        `DEXCLI is not an executable file: ${environment.dexcliPath}. Unset DEXCLI to use the dexcli on PATH or ` +
          "the downloaded release, or set it to the path of a dexcli executable.",
      );
    }
    return environment.dexcliPath;
  }
  const installDirectory = join(environment.binDirectory, `dexcli-${pinnedDexcliVersion}`);
  const cached = join(installDirectory, "dexcli");
  if (existsSync(installDirectory)) {
    await verifyInstalledFiles(installDirectory);
    return cached;
  }
  const onPath = await findDexcliOnPath(environment.executableSearchPath);
  if (onPath !== undefined) {
    return onPath;
  }
  await downloadDexcli(environment, installDirectory, log);
  return cached;
}

async function findDexcliOnPath(searchPath: readonly string[]): Promise<string | undefined> {
  for (const directory of searchPath) {
    const candidate = join(directory, "dexcli");
    if (isExecutable(candidate)) {
      const version = await readDexcliVersion(candidate);
      if (version !== undefined && compareVersions(version, pinnedDexcliVersion) >= 0) {
        return candidate;
      }
    }
  }
  return undefined;
}

async function readDexcliVersion(dexcliPath: string): Promise<string | undefined> {
  try {
    const { stdout } = await execFileAsync(dexcliPath, ["--version"], { timeout: 10_000 });
    return /v?(\d+\.\d+\.\d+)/.exec(stdout)?.[1];
  } catch {
    return undefined;
  }
}

async function downloadDexcli(environment: CliEnvironment, installDirectory: string, log: (line: string) => void) {
  requireTrustedDownloadUrl(environment.dexcliDownloadUrl);
  const archiveName = `dexcli_v${pinnedDexcliVersion}_${releasePlatform()}.tar.gz`;
  const releaseUrl = `${environment.dexcliDownloadUrl.replace(/\/+$/, "")}/cli-v${pinnedDexcliVersion}`;
  log(`Downloading dexcli ${pinnedDexcliVersion} to ${installDirectory}; this happens once.`);
  const [archive, checksums] = await Promise.all([
    download(`${releaseUrl}/${archiveName}`),
    download(`${releaseUrl}/checksums.txt`),
  ]);
  const expected = findChecksum(checksums.toString("utf8"), archiveName);
  const actual = sha256Of(archive);
  if (expected !== actual) {
    throw new Error(`the downloaded ${archiveName} has SHA-256 ${actual}, but checksums.txt says ${expected}`);
  }
  let files: Map<string, Buffer>;
  try {
    files = readWantedTarFiles(archive, installedExecutables);
  } catch (error: unknown) {
    throw new Error(`${archiveName} was refused: ${error instanceof Error ? error.message : String(error)}`, {
      cause: error,
    });
  }
  await mkdir(environment.binDirectory, { recursive: true });
  const staging = await mkdtemp(join(environment.binDirectory, `.dexcli-${pinnedDexcliVersion}-`));
  try {
    const digests: Record<string, string> = {};
    for (const name of installedExecutables) {
      const content = files.get(name) ?? Buffer.alloc(0);
      await writeFile(join(staging, name), content, { mode: 0o755, flag: "wx" });
      digests[name] = sha256Of(content);
    }
    await writeFile(
      join(staging, installRecordName),
      `${JSON.stringify({ version: pinnedDexcliVersion, archive: archiveName, archiveSha256: actual, sha256: digests }, null, 2)}\n`,
      { flag: "wx" },
    );
    await rename(staging, installDirectory).catch(async (error: unknown) => {
      if (!existsSync(installDirectory)) {
        throw error;
      }
      // Another command installed it first; use that copy after the same check.
      await verifyInstalledFiles(installDirectory);
    });
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}

/** Checks that each installed executable still has the digest recorded at install. */
async function verifyInstalledFiles(installDirectory: string): Promise<void> {
  const fail = (reason: string): never => {
    throw new Error(
      `the cached dexcli in ${installDirectory} ${reason}; delete that directory to download dexcli again`,
    );
  };
  let record: { sha256?: Record<string, unknown> };
  try {
    record = JSON.parse(await readFile(join(installDirectory, installRecordName), "utf8")) as typeof record;
  } catch {
    return fail(`has no readable ${installRecordName}`);
  }
  for (const name of installedExecutables) {
    const path = join(installDirectory, name);
    const stats = lstatSync(path, { throwIfNoEntry: false });
    if (stats === undefined || !stats.isFile() || !isExecutable(path)) {
      fail(`has no executable regular file ${name}`);
    }
    if (record.sha256?.[name] !== sha256Of(await readFile(path))) {
      fail(`has a ${name} that changed since it was installed`);
    }
  }
}

/** Requires an `https` download URL, or `http` on a loopback host for development and tests. */
function requireTrustedDownloadUrl(downloadUrl: string): void {
  let url: URL;
  try {
    url = new URL(downloadUrl);
  } catch {
    throw new Error(`DEX_AGENTS_DEXCLI_DOWNLOAD_URL is not a URL: ${downloadUrl}`);
  }
  const isLoopback = url.hostname === "127.0.0.1" || url.hostname === "localhost" || url.hostname === "[::1]";
  if (url.protocol !== "https:" && !(url.protocol === "http:" && isLoopback)) {
    throw new Error(
      `DEX_AGENTS_DEXCLI_DOWNLOAD_URL must be an https URL, or http on 127.0.0.1, localhost, or [::1]: ${downloadUrl}`,
    );
  }
}

function findChecksum(checksums: string, archiveName: string): string {
  for (const line of checksums.split("\n")) {
    const match = /^([0-9a-f]{64}) {2}(?:\.\/)?(\S+)$/.exec(line.trim());
    if (match?.[2] === archiveName && match[1] !== undefined) {
      return match[1];
    }
  }
  throw new Error(`checksums.txt has no entry for ${archiveName}`);
}

function sha256Of(content: Buffer): string {
  return createHash("sha256").update(content).digest("hex");
}

/** Downloads a URL, following at most five redirects, each of which must also be a trusted download URL. */
async function download(url: string): Promise<Buffer> {
  let current = url;
  for (let hop = 0; hop <= maximumRedirects; hop += 1) {
    let response: Response;
    try {
      response = await fetch(current, { redirect: "manual", signal: AbortSignal.timeout(downloadTimeoutMs) });
    } catch (error: unknown) {
      throw new Error(`downloading ${current} failed: ${error instanceof Error ? error.message : String(error)}`, {
        cause: error,
      });
    }
    const location = response.headers.get("location");
    if (response.status >= 300 && response.status < 400 && location !== null) {
      await response.arrayBuffer().catch(() => undefined);
      const next = new URL(location, current).href;
      try {
        requireTrustedDownloadUrl(next);
      } catch (error: unknown) {
        throw new Error(`downloading ${url} was refused: it redirects to ${next}, which is not https or loopback http`, {
          cause: error,
        });
      }
      current = next;
      continue;
    }
    if (!response.ok) {
      throw new Error(`downloading ${current} failed with HTTP ${response.status}`);
    }
    return Buffer.from(await response.arrayBuffer());
  }
  throw new Error(`downloading ${url} was refused: more than ${maximumRedirects} redirects`);
}

function releasePlatform(): string {
  const operatingSystem = { darwin: "darwin", linux: "linux" }[process.platform as string];
  const architecture = { x64: "amd64", arm64: "arm64" }[process.arch as string];
  if (operatingSystem === undefined || architecture === undefined) {
    throw new Error(
      `dexcli has no release for ${process.platform}/${process.arch}; install dexcli and set DEXCLI to its path`,
    );
  }
  return `${operatingSystem}_${architecture}`;
}

function isExecutable(path: string): boolean {
  try {
    accessSync(path, constants.X_OK);
    return statSync(path).isFile();
  } catch {
    return false;
  }
}

function compareVersions(left: string, right: string): number {
  const leftParts = left.split(".").map(Number);
  const rightParts = right.split(".").map(Number);
  for (let index = 0; index < 3; index += 1) {
    const difference = (leftParts[index] ?? 0) - (rightParts[index] ?? 0);
    if (difference !== 0) {
      return difference;
    }
  }
  return 0;
}
