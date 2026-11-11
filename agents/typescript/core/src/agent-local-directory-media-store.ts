// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomBytes } from "node:crypto";
import { constants, type Stats } from "node:fs";
import { lstat, mkdir, open, realpath, rename, stat, unlink, type FileHandle } from "node:fs/promises";
import { posix } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import {
  MediaStoreError,
  buildImageReference,
  requireShortImageUris,
  requireStoreImageReference,
  verifyImageBytes,
  type MediaStore,
} from "./agent-media.js";
import type { AgentImageReference } from "./agent-session-values.js";

const imageDirectoryName = "sha256";
const directoryMode = 0o700;
const fileMode = 0o600;
// Errors of an O_NOFOLLOW | O_NONBLOCK open that name a symbolic link or a non-regular file.
const notRegularFileErrorCodes: ReadonlySet<string> = new Set(["ELOOP", "EISDIR", "ENXIO", "EOPNOTSUPP"]);
// Opens to try, and milliseconds between them, while concurrent putImage calls replace the file.
const openAttempts = 10;
const openRetryDelayMs = 5;

/**
 * Keeps image bytes in a local directory, one file per distinct image.
 *
 * Use it when every client and Worker of a session runs on one machine, such as a command-line tool in local mode;
 * use `S3MediaStore` from `@superdurable/dex-agents/s3` when they run on different machines.
 *
 * The file of an image is `<root>/sha256/<sha256>`, and a reference's `uri` is `file://<root>/sha256/<sha256>`, where
 * `<root>` is the absolute root with every UTF-8 byte other than ASCII letters, digits, `-`, `.`, `_`, `~`, and `/`
 * written as `%XX` in uppercase hex. The root is made absolute as text: `.` and `..` segments and repeated or trailing
 * `/` characters are resolved without reading the file system, symbolic links are not resolved, and Unicode is not
 * normalized. A client and a Worker that pass the same root therefore build the same references, in every core.
 *
 * `putImage` copies the bytes into a temporary file in the same directory, syncs it, and renames it into place, so a
 * reader never sees a partial image. An existing file with the same bytes is success, so retried and concurrent uploads
 * of the same bytes leave one file; an existing file with other bytes is replaced. Directories the store creates get
 * mode `0700` and image files mode `0600`, narrowed further by the process umask. The store syncs the parent of every
 * directory it creates, and the `sha256` directory after each rename, so a returned reference survives a crash.
 *
 * The store refuses a `sha256` directory or an image file that is a symbolic link or not a regular file, and an image
 * file with more than one hard link, with `MediaStoreError`, on both upload and read. The root itself may be a symbolic
 * link. Node.js has no `openat`, so this store opens files by path and checks before and after each operation: it
 * checks the `sha256` directory with `lstat` and opens the file with `O_NOFOLLOW`, then confirms that the real path of
 * the `sha256` directory is still the root's and that the path still names the opened or written file. A read that
 * fails the check returns nothing, and a write that fails it removes the file it wrote when it can still find it. A
 * process of the same user that can write the `0700` root can still race a directory swap between these checks; the
 * Python core opens every path relative to an open directory and has no such window. `getImage` reads only
 * references under this store's root, refuses a file whose size differs from the reference before reading it, reads at
 * most one byte more than `sizeBytes`, and checks the bytes with `verifyImageBytes`. It needs a POSIX file system; on
 * Windows the constructor throws.
 *
 * Session deletion does not delete files. A runner reads a session's images again whenever a model call replays them,
 * so keep every file at least as long as any session that references it can still run a turn or replay its history. A
 * missing file makes those turns fail with `MediaStoreError`.
 *
 * @example
 * ```ts
 * import { LocalDirectoryMediaStore } from "@superdurable/dex-agents";
 *
 * const store = new LocalDirectoryMediaStore("/var/lib/my-agent/media");
 * const reference = await store.putImage(screenshotPng);
 * // reference.uri === "file:///var/lib/my-agent/media/sha256/9f2c..."
 * ```
 * @since 0.5.0
 */
export class LocalDirectoryMediaStore implements MediaStore {
  /** The absolute root directory, made absolute as text, without a trailing `/`. */
  public readonly root: string;
  /** The location that every reference of this store starts with: `"file://<root>"`, with the root percent-encoded. */
  public readonly storeUri: string;

  /**
   * Creates a store; it touches no file until the first upload or read.
   *
   * @param root - Absolute path of the directory that holds the images, such as `"/var/lib/my-agent/media"`. The first
   * upload creates it and any missing parent directory with mode `0700`.
   * @throws {TypeError} When `root` is not absolute, is not well-formed Unicode, contains NUL, or is the file system
   * root.
   * @throws {RangeError} When `root` makes image URIs longer than `maxImageUriLength`.
   * @throws {Error} On a system without POSIX file access, such as Windows.
   */
  public constructor(root: string) {
    if (process.platform === "win32" || constants.O_NOFOLLOW === undefined) {
      throw new Error("LocalDirectoryMediaStore needs a POSIX file system");
    }
    this.root = absoluteRoot(root);
    this.storeUri = `file://${percentEncodePath(this.root)}`;
    requireShortImageUris(this.storeUri);
  }

  /**
   * Returns the path of the file of the image with this SHA-256.
   *
   * @param sha256 - SHA-256 of the bytes as 64 lowercase hex digits.
   * @returns `"<root>/sha256/<sha256>"`.
   */
  public imagePath(sha256: string): string {
    return `${this.imageDirectory()}/${sha256}`;
  }

  /**
   * Stores one image unless the directory already has it, and returns its reference.
   *
   * @param data - PNG, JPEG, GIF, or WebP bytes, at most `maxImageBytes`.
   * @returns The reference.
   * @throws {ImageValidationError} When the bytes are not a valid image for `buildImageReference`. Nothing is written.
   * @throws {MediaStoreError} When the `sha256` directory or the image's file is a symbolic link or not a directory or
   * regular file, the image's file has more than one hard link, or the `sha256` directory changed during the write.
   * @throws A Node.js system error (`NodeJS.ErrnoException`) when the file system refuses a write, such as for missing
   * permissions or a full disk. Retrying is safe.
   */
  public async putImage(data: Uint8Array): Promise<AgentImageReference> {
    const reference = buildImageReference(data, this.storeUri);
    const copy = Uint8Array.from(data);
    await this.makeRoot();
    try {
      await this.requireImageDirectory();
    } catch (error) {
      if (!isSystemError(error, "ENOENT")) {
        throw error;
      }
      await makeDirectory(this.imageDirectory());
      // Also after EEXIST: the racing creator may not have synced it yet.
      await syncDirectory(this.root);
      await this.requireImageDirectory();
    }
    if (!(await this.hasImageFile(reference, copy))) {
      await this.replaceImageFile(reference, copy);
    }
    return reference;
  }

  /**
   * Returns the bytes of one image in this store.
   *
   * @param reference - A reference under this store's root.
   * @returns The exact bytes, checked with `verifyImageBytes`.
   * @throws {ImageValidationError} When the reference's fields are invalid.
   * @throws {MediaStoreError} When the reference is outside this store's root, the file is missing, is a symbolic link
   * or not a regular file, has more than one hard link, moved during the read, or its bytes do not match.
   * @throws A Node.js system error (`NodeJS.ErrnoException`) when the file system refuses the read for another reason.
   */
  public async getImage(reference: AgentImageReference): Promise<Uint8Array> {
    requireStoreImageReference(reference, this.storeUri);
    const missing = (cause: unknown): MediaStoreError =>
      new MediaStoreError(`image ${reference.uri} is not in the media store`, { cause });
    const mismatch = (): MediaStoreError =>
      new MediaStoreError(`image ${reference.uri} does not match its sha256 and size`);
    let handle: FileHandle;
    try {
      await this.requireImageDirectory();
      handle = await this.openImageFile(reference);
    } catch (error) {
      throw isSystemError(error, "ENOENT") ? missing(error) : error;
    }
    let data: Uint8Array;
    try {
      if ((await handle.stat()).size !== reference.sizeBytes) {
        throw mismatch();
      }
      // One byte more than expected tells a file that grew apart without reading all of it.
      data = await readAtMost(handle, reference.sizeBytes + 1);
    } finally {
      await handle.close();
    }
    if (data.length !== reference.sizeBytes) {
      throw mismatch();
    }
    return verifyImageBytes(reference, data);
  }

  private imageDirectory(): string {
    return `${this.root}/${imageDirectoryName}`;
  }

  /** Creates the root and its missing parents with mode `0700`, syncing the parent of each. */
  private async makeRoot(): Promise<void> {
    const missing: string[] = [];
    let path = this.root;
    while (!(await isDirectory(path))) {
      missing.push(path);
      path = posix.dirname(path);
    }
    for (const directory of missing.reverse()) {
      await makeDirectory(directory);
      await syncDirectory(posix.dirname(directory));
    }
  }

  /** Throws `MediaStoreError` unless the `sha256` directory is a real directory, not a symbolic link. */
  private async requireImageDirectory(): Promise<void> {
    const stats = await lstat(this.imageDirectory());
    if (!stats.isDirectory()) {
      throw new MediaStoreError(
        `media store directory ${this.imageDirectory()} is a symbolic link or not a directory`,
      );
    }
  }

  /** Returns whether the image's file exists and holds exactly `data`. */
  private async hasImageFile(reference: AgentImageReference, data: Uint8Array): Promise<boolean> {
    let handle: FileHandle;
    try {
      handle = await this.openImageFile(reference);
    } catch (error) {
      if (isSystemError(error, "ENOENT")) {
        return false;
      }
      throw error;
    }
    try {
      if ((await handle.stat()).size !== data.length) {
        return false;
      }
      return Buffer.from(await readAtMost(handle, data.length + 1)).equals(data);
    } finally {
      await handle.close();
    }
  }

  /** Opens the reference's image file, refusing symbolic links, special files, hard links, and moved files. */
  private async openImageFile(reference: AgentImageReference): Promise<FileHandle> {
    for (let attempt = 0; attempt < openAttempts; attempt += 1) {
      const handle = await this.openImageFileOnce(reference);
      if (handle !== undefined) {
        return handle;
      }
      await delay(openRetryDelayMs * (attempt + 1));
    }
    throw new MediaStoreError(`image ${reference.uri} was replaced while the media store opened it`);
  }

  /** Returns the open file, or `undefined` when a rename replaced it after the open. */
  private async openImageFileOnce(reference: AgentImageReference): Promise<FileHandle | undefined> {
    const notRegularFile = (cause?: unknown): MediaStoreError =>
      new MediaStoreError(`image ${reference.uri} is a symbolic link or not a regular file in the media store`, {
        cause,
      });
    let handle: FileHandle;
    try {
      // O_NONBLOCK keeps a planted FIFO from blocking the open.
      handle = await open(
        this.imagePath(reference.sha256),
        constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
      );
    } catch (error) {
      if (isSystemError(error, ...notRegularFileErrorCodes)) {
        throw notRegularFile(error);
      }
      throw error;
    }
    const stats = await handle.stat();
    if (!stats.isFile()) {
      await handle.close();
      throw notRegularFile();
    }
    if (stats.nlink === 0) {
      await handle.close();
      return undefined;
    }
    if (stats.nlink !== 1) {
      await handle.close();
      // Another name for the inode may lie outside the root.
      throw new MediaStoreError(`image ${reference.uri} has more than one hard link in the media store`);
    }
    if (!(await this.isImageDirectoryInStore())) {
      await handle.close();
      throw this.directoryChanged(reference);
    }
    if (!(await isSameFile(this.imagePath(reference.sha256), stats))) {
      // A concurrent rename replaced the file after the open; the caller opens it again.
      await handle.close();
      return undefined;
    }
    return handle;
  }

  /** Returns whether the `sha256` directory is a real directory whose real path is the root's. */
  private async isImageDirectoryInStore(): Promise<boolean> {
    try {
      if (!(await lstat(this.imageDirectory())).isDirectory()) {
        return false;
      }
      const expectedDirectory = posix.join(await realpath(this.root), imageDirectoryName);
      return (await realpath(this.imageDirectory())) === expectedDirectory;
    } catch (error) {
      if (isSystemError(error, "ENOENT", "ENOTDIR", "ELOOP")) {
        return false;
      }
      throw error;
    }
  }

  private directoryChanged(reference: AgentImageReference): MediaStoreError {
    return new MediaStoreError(
      `media store directory ${this.imageDirectory()} changed while image ${reference.uri} was read or written`,
    );
  }

  private async replaceImageFile(reference: AgentImageReference, data: Uint8Array): Promise<void> {
    const directory = this.imageDirectory();
    const imagePath = this.imagePath(reference.sha256);
    const temporaryPath = `${directory}/.${reference.sha256}.${randomBytes(8).toString("hex")}.tmp`;
    const handle = await open(
      temporaryPath,
      constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW,
      fileMode,
    );
    let written: Stats;
    try {
      try {
        await handle.writeFile(data);
        await handle.sync();
        written = await handle.stat();
      } finally {
        await handle.close();
      }
      // rename replaces the entry itself, never a link's target, and is atomic.
      await rename(temporaryPath, imagePath);
    } catch (error) {
      // The original error matters more than a temporary file left behind.
      await unlink(temporaryPath).catch(() => undefined);
      throw error;
    }
    if (!(await this.isImageDirectoryInStore())) {
      await removeIfSameFile(imagePath, written);
      throw this.directoryChanged(reference);
    }
    // A concurrent putImage may have renamed its own copy over ours; accept it only when it holds these bytes.
    if (!(await isSameFile(imagePath, written)) && !(await this.hasImageFile(reference, data))) {
      throw this.directoryChanged(reference);
    }
    await syncDirectory(directory);
  }
}

async function isSameFile(path: string, file: Stats): Promise<boolean> {
  try {
    const placed = await lstat(path);
    return placed.dev === file.dev && placed.ino === file.ino;
  } catch (error) {
    if (isSystemError(error, "ENOENT", "ENOTDIR", "ELOOP")) {
      return false;
    }
    throw error;
  }
}

async function isDirectory(path: string): Promise<boolean> {
  try {
    return (await stat(path)).isDirectory();
  } catch {
    // Any failure means "create it"; mkdir then reports the real error.
    return false;
  }
}

async function makeDirectory(path: string): Promise<void> {
  try {
    await mkdir(path, { mode: directoryMode });
  } catch (error) {
    if (!isSystemError(error, "EEXIST")) {
      throw error;
    }
  }
}

async function syncDirectory(path: string): Promise<void> {
  const handle = await open(path, constants.O_RDONLY);
  try {
    await handle.sync();
  } finally {
    await handle.close();
  }
}

/** Removes the file at `path` when it is still `file`, wherever the path now leads. */
async function removeIfSameFile(path: string, file: Stats): Promise<void> {
  try {
    const placed = await lstat(path);
    if (placed.dev === file.dev && placed.ino === file.ino) {
      await unlink(path);
    }
  } catch {
    // Best effort: the caller already throws, and the file may be gone.
  }
}

function absoluteRoot(root: string): string {
  if (!root.startsWith("/")) {
    throw new TypeError("LocalDirectoryMediaStore root must be an absolute path");
  }
  // In a u-mode pattern, only a lone surrogate matches this range.
  if (/[\uD800-\uDFFF]/u.test(root)) {
    throw new TypeError("LocalDirectoryMediaStore root must be well-formed Unicode");
  }
  if (root.includes("\0")) {
    throw new TypeError("LocalDirectoryMediaStore root must not contain NUL");
  }
  const absolute = posix.resolve(root);
  if (absolute === "/") {
    throw new TypeError("LocalDirectoryMediaStore root must not be the file system root");
  }
  return absolute;
}

/** Writes every UTF-8 byte other than ASCII letters, digits, `-._~` and `/` as `%XX`, as Python's `quote` does. */
function percentEncodePath(path: string): string {
  let encoded = "";
  for (const byte of Buffer.from(path, "utf8")) {
    const character = String.fromCharCode(byte);
    encoded += /^[A-Za-z0-9\-._~\/]$/.test(character)
      ? character
      : `%${byte.toString(16).toUpperCase().padStart(2, "0")}`;
  }
  return encoded;
}

async function readAtMost(handle: FileHandle, limit: number): Promise<Uint8Array> {
  const buffer = Buffer.alloc(limit);
  let total = 0;
  while (total < limit) {
    const { bytesRead } = await handle.read(buffer, total, limit - total, null);
    if (bytesRead === 0) {
      break;
    }
    total += bytesRead;
  }
  return new Uint8Array(buffer.buffer, buffer.byteOffset, total);
}

function isSystemError(error: unknown, ...codes: string[]): boolean {
  return error instanceof Error && codes.includes((error as NodeJS.ErrnoException).code ?? "");
}
