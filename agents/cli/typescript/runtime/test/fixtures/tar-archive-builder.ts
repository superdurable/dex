// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { gzipSync } from "node:zlib";

/** One entry of a test archive. */
export interface TarEntry {
  /** The entry name, written as is, so tests can write unsafe names. */
  readonly name: string;
  /** The entry type flag: `0` regular file, `2` symbolic link, `5` directory, and so on. Defaults to `0`. */
  readonly typeFlag?: string;
  /** The file content. Defaults to empty. */
  readonly content?: string;
  /** The link target of a link entry. */
  readonly linkName?: string;
}

/**
 * Builds a gzipped ustar archive from entries, without any checks, for archive-handling tests.
 * @param entries - The entries, in order.
 * @returns The `.tar.gz` bytes.
 */
export function buildTarGz(entries: readonly TarEntry[]): Buffer {
  const blocks: Buffer[] = [];
  for (const entry of entries) {
    const content = Buffer.from(entry.content ?? "", "utf8");
    const header = Buffer.alloc(512);
    header.write(entry.name, 0, 100, "utf8");
    header.write("0000755\0", 100, "latin1");
    header.write("0000000\0", 108, "latin1");
    header.write("0000000\0", 116, "latin1");
    header.write(`${content.length.toString(8).padStart(11, "0")}\0`, 124, "latin1");
    header.write("00000000000\0", 136, "latin1");
    header.write("        ", 148, "latin1");
    header.write(entry.typeFlag ?? "0", 156, "latin1");
    header.write(entry.linkName ?? "", 157, 100, "utf8");
    header.write("ustar\0", 257, "latin1");
    header.write("00", 263, "latin1");
    let sum = 0;
    for (const byte of header) {
      sum += byte;
    }
    header.write(`${sum.toString(8).padStart(6, "0")}\0 `, 148, "latin1");
    blocks.push(header, content, Buffer.alloc((512 - (content.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return gzipSync(Buffer.concat(blocks));
}
