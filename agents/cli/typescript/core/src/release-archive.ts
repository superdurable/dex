// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { gunzipSync } from "node:zlib";

const blockSize = 512;
const maximumUnpackedBytes = 1024 * 1024 * 1024;

/**
 * Returns the contents of the named regular files of a gzipped tar archive, without writing anything.
 *
 * Every entry name must be relative, without a `..` component, a backslash, or a NUL. Every entry must be a
 * regular file, a directory, or a PAX or GNU long-name header; a link, a device, or any other kind rejects the
 * whole archive. Each wanted file must appear exactly once. Other regular files and directories are skipped.
 *
 * @param archive - The `.tar.gz` bytes.
 * @param wantedNames - The file names to return, such as `dexcli`.
 * @returns The wanted files' contents by name.
 * @throws {Error} When the archive is malformed, has a rejected entry, or lacks or repeats a wanted file.
 */
export function readWantedTarFiles(archive: Buffer, wantedNames: readonly string[]): Map<string, Buffer> {
  const tar = gunzipSync(archive, { maxOutputLength: maximumUnpackedBytes });
  const files = new Map<string, Buffer>();
  let offset = 0;
  let pendingLongName: string | undefined;
  while (offset + blockSize <= tar.length) {
    const header = tar.subarray(offset, offset + blockSize);
    if (header.every((byte) => byte === 0)) {
      break;
    }
    requireValidChecksum(header);
    const size = readOctal(header.subarray(124, 136));
    const typeFlag = String.fromCharCode(header[156] ?? 0);
    const dataStart = offset + blockSize;
    const dataEnd = dataStart + size;
    if (dataEnd > tar.length) {
      throw new Error("the archive ends inside an entry");
    }
    const data = tar.subarray(dataStart, dataEnd);
    offset = dataStart + Math.ceil(size / blockSize) * blockSize;
    if (typeFlag === "x" || typeFlag === "L") {
      pendingLongName = typeFlag === "x" ? readPaxPath(data) : readCString(data);
      continue;
    }
    if (typeFlag === "g") {
      continue;
    }
    const name = pendingLongName ?? readHeaderName(header);
    pendingLongName = undefined;
    const normalized = requireSafeEntryName(name);
    if (typeFlag === "5") {
      continue;
    }
    if (typeFlag !== "0" && typeFlag !== "\0") {
      throw new Error(`the archive entry ${name} is not a regular file or a directory (type ${JSON.stringify(typeFlag)})`);
    }
    if (wantedNames.includes(normalized)) {
      if (files.has(normalized)) {
        throw new Error(`the archive has ${normalized} twice`);
      }
      files.set(normalized, Buffer.from(data));
    }
  }
  for (const name of wantedNames) {
    if (!files.has(name)) {
      throw new Error(`the archive has no regular file ${name}`);
    }
  }
  return files;
}

function requireSafeEntryName(name: string): string {
  const withoutDotSlash = name.replace(/^(?:\.\/)+/, "").replace(/\/+$/, "");
  if (
    name === "" ||
    name.startsWith("/") ||
    name.includes("\\") ||
    name.includes("\0") ||
    withoutDotSlash.split("/").some((component) => component === "..")
  ) {
    throw new Error(`the archive entry ${JSON.stringify(name)} is not a safe relative path`);
  }
  return withoutDotSlash;
}

function readHeaderName(header: Buffer): string {
  const name = readCString(header.subarray(0, 100));
  const isUstar = header.subarray(257, 262).toString("latin1") === "ustar";
  const prefix = isUstar ? readCString(header.subarray(345, 500)) : "";
  return prefix === "" ? name : `${prefix}/${name}`;
}

function readPaxPath(data: Buffer): string | undefined {
  const text = data.toString("utf8");
  let offset = 0;
  while (offset < text.length) {
    const space = text.indexOf(" ", offset);
    const length = Number(text.slice(offset, space));
    if (space < 0 || !Number.isSafeInteger(length) || length <= 0) {
      throw new Error("the archive has a malformed PAX header");
    }
    const record = text.slice(space + 1, offset + length - 1);
    if (record.startsWith("path=")) {
      return record.slice("path=".length);
    }
    offset += length;
  }
  return undefined;
}

function readCString(bytes: Buffer): string {
  const end = bytes.indexOf(0);
  return bytes.subarray(0, end < 0 ? bytes.length : end).toString("utf8");
}

function readOctal(bytes: Buffer): number {
  const text = readCString(bytes).trim();
  if (!/^[0-7]*$/.test(text)) {
    throw new Error("the archive has a malformed size field");
  }
  return text === "" ? 0 : Number.parseInt(text, 8);
}

function requireValidChecksum(header: Buffer): void {
  const expected = readOctal(header.subarray(148, 156));
  let sum = 0;
  for (let index = 0; index < blockSize; index += 1) {
    sum += index >= 148 && index < 156 ? 0x20 : (header[index] ?? 0);
  }
  if (sum !== expected) {
    throw new Error("the archive has a header with a wrong checksum");
  }
}
