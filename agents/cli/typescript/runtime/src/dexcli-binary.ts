// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFile, spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { accessSync, constants, existsSync, statSync } from "node:fs";
import { mkdir, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";

import type { CliEnvironment } from "./cli-environment.js";

/**
 * The `dexcli` release a command downloads when it finds no `dexcli` of at least this version.
 *
 * @since 0.5.0
 */
export const pinnedDexcliVersion = "1.42.0";

const downloadTimeoutMs = 300_000;
const execFileAsync = promisify(execFile);

/**
 * Returns a `dexcli` executable, downloading the pinned release on first use.
 *
 * The order is: `DEXCLI`; `~/.dex/bin/dexcli-<version>/dexcli` from an earlier download; `dexcli` on `PATH` when
 * its version is at least {@link pinnedDexcliVersion}; otherwise the pinned release for this platform, downloaded
 * from `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` (GitHub releases by default). The archive must match the release's
 * `checksums.txt` SHA-256 before it is unpacked into `~/.dex/bin`.
 *
 * @param environment - The command's environment settings.
 * @param log - Writes progress for the person at the terminal.
 * @returns The executable's path; `dex-server-zd` sits next to it unless it came from `PATH`.
 * @throws {Error} When `DEXCLI` is not executable, the platform has no release, or the download or checksum
 * fails.
 * @since 0.5.0
 */
export async function resolveDexcliExecutable(
  environment: CliEnvironment,
  log: (line: string) => void,
): Promise<string> {
  if (environment.dexcliPath !== undefined) {
    if (!isExecutable(environment.dexcliPath)) {
      throw new Error(`DEXCLI is not an executable file: ${environment.dexcliPath}`);
    }
    return environment.dexcliPath;
  }
  const installDirectory = join(environment.binDirectory, `dexcli-${pinnedDexcliVersion}`);
  const cached = join(installDirectory, "dexcli");
  if (isExecutable(cached)) {
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
      return version !== undefined && compareVersions(version, pinnedDexcliVersion) >= 0 ? candidate : undefined;
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
  const archiveName = `dexcli_v${pinnedDexcliVersion}_${releasePlatform()}.tar.gz`;
  const releaseUrl = `${environment.dexcliDownloadUrl.replace(/\/+$/, "")}/cli-v${pinnedDexcliVersion}`;
  log(`Downloading dexcli ${pinnedDexcliVersion} to ${installDirectory}; this happens once.`);
  const [archive, checksums] = await Promise.all([
    download(`${releaseUrl}/${archiveName}`),
    download(`${releaseUrl}/checksums.txt`),
  ]);
  const expected = findChecksum(checksums.toString("utf8"), archiveName);
  const actual = createHash("sha256").update(archive).digest("hex");
  if (expected !== actual) {
    throw new Error(`the downloaded ${archiveName} has SHA-256 ${actual}, but checksums.txt says ${expected}`);
  }
  await mkdir(environment.binDirectory, { recursive: true });
  const staging = join(environment.binDirectory, `.dexcli-${pinnedDexcliVersion}-${randomBytes(4).toString("hex")}`);
  await mkdir(staging);
  try {
    const archivePath = join(staging, archiveName);
    await writeFile(archivePath, archive);
    await extractTarGz(archivePath, staging);
    for (const executable of ["dexcli", "dex-server-zd"]) {
      if (!statSync(join(staging, executable), { throwIfNoEntry: false })?.isFile()) {
        throw new Error(`${archiveName} has no ${executable}`);
      }
    }
    await rm(archivePath);
    await rename(staging, installDirectory).catch((error: unknown) => {
      if (!existsSync(join(installDirectory, "dexcli"))) {
        throw error;
      }
    });
  } finally {
    await rm(staging, { recursive: true, force: true });
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

async function download(url: string): Promise<Buffer> {
  let response: Response;
  try {
    response = await fetch(url, { signal: AbortSignal.timeout(downloadTimeoutMs) });
  } catch (error: unknown) {
    throw new Error(`downloading ${url} failed: ${error instanceof Error ? error.message : String(error)}`, {
      cause: error,
    });
  }
  if (!response.ok) {
    throw new Error(`downloading ${url} failed with HTTP ${response.status}`);
  }
  return Buffer.from(await response.arrayBuffer());
}

function extractTarGz(archivePath: string, directory: string): Promise<void> {
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn("tar", ["-xzf", archivePath, "-C", directory], { stdio: ["ignore", "ignore", "pipe"] });
    let stderr = "";
    child.stderr.on("data", (chunk: Buffer) => {
      stderr += chunk.toString("utf8");
    });
    child.once("error", rejectPromise);
    child.once("close", (code) =>
      code === 0 ? resolvePromise() : rejectPromise(new Error(`tar exited with ${code}: ${stderr.trim()}`)),
    );
  });
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
