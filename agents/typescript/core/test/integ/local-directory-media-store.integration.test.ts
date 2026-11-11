// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// LocalDirectoryMediaStore: the shared location cases, and its files on a real file system.

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  existsSync,
  linkSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { open, type FileHandle } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, describe, test } from "node:test";

import {
  LocalDirectoryMediaStore,
  buildImageReference,
  imageUri,
  type AgentImageReference,
} from "../../src/index.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";

interface LocalDirectorySuite {
  readonly images: readonly { readonly name: string; readonly base64: string }[];
  readonly store_locations: {
    readonly sha256: string;
    readonly local_directory: readonly {
      readonly name: string;
      readonly root: string;
      readonly store_uri: string;
      readonly image_path: string;
      readonly image_uri: string;
    }[];
    readonly local_directory_rejected_roots: readonly {
      readonly name: string;
      readonly root: string;
      readonly error: string;
    }[];
    readonly local_directory_too_long: readonly {
      readonly name: string;
      readonly root: string;
      readonly store_error: string;
    }[];
  };
  readonly local_directory_store_errors: {
    readonly missing: string;
    readonly not_regular_file: string;
    readonly not_directory: string;
    readonly hard_link: string;
  };
}

const suite = readConformanceJson<LocalDirectorySuite>("images.json");
const locations = suite.store_locations;
const errors = suite.local_directory_store_errors;
const images = new Map(suite.images.map((image) => [image.name, new Uint8Array(Buffer.from(image.base64, "base64"))]));
const png = images.get("png") as Uint8Array;
const jpeg = images.get("jpeg") as Uint8Array;

const directories: string[] = [];

after(() => {
  for (const directory of directories) {
    rmSync(directory, { recursive: true, force: true });
  }
});

function newDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), "dex-agents-local-media-"));
  directories.push(directory);
  return directory;
}

function imageFileNames(root: string): string[] {
  const directory = join(root, "sha256");
  return existsSync(directory) ? readdirSync(directory).sort() : [];
}

function modeOf(path: string): number {
  return lstatSync(path).mode & 0o777;
}

function withUri(reference: AgentImageReference, uri: string): AgentImageReference {
  return { ...reference, uri };
}

type FileHandleMethod = "stat" | "sync";

/** Runs `body` while `method` of every FileHandle first runs `before`; restores the method afterwards. */
async function withFileHandleHook<T>(
  method: FileHandleMethod,
  before: (handle: FileHandle) => Promise<void>,
  body: () => Promise<T>,
): Promise<T> {
  const probe = await open(tmpdir(), "r");
  const prototype = Object.getPrototypeOf(probe) as Record<FileHandleMethod, (...args: unknown[]) => Promise<unknown>>;
  await probe.close();
  const original = prototype[method];
  prototype[method] = async function (this: FileHandle, ...args: unknown[]): Promise<unknown> {
    await before(this);
    return original.apply(this, args);
  };
  try {
    return await body();
  } finally {
    prototype[method] = original;
  }
}

/** Moves `<root>/sha256` aside and puts a symbolic link to `target` in its place. */
function swapImageDirectory(root: string, target: string): void {
  renameSync(join(root, "sha256"), join(root, "sha256-moved"));
  symlinkSync(target, join(root, "sha256"));
}

describe("LocalDirectoryMediaStore conformance cases", () => {
  for (const location of locations.local_directory) {
    test(`a local directory store names images by content: ${location.name}`, () => {
      const store = new LocalDirectoryMediaStore(location.root);
      assert.equal(store.storeUri, location.store_uri);
      assert.equal(store.imagePath(locations.sha256), location.image_path);
      assert.equal(imageUri(store.storeUri, locations.sha256), location.image_uri);
    });
  }

  for (const rejected of locations.local_directory_rejected_roots) {
    test(`a root that is not a usable absolute path is refused: ${rejected.name}`, () => {
      assert.throws(() => new LocalDirectoryMediaStore(rejected.root), { name: "TypeError", message: rejected.error });
    });
  }

  for (const tooLong of locations.local_directory_too_long) {
    test(`a root too long for its image URIs is refused: ${tooLong.name}`, () => {
      assert.throws(() => new LocalDirectoryMediaStore(tooLong.root), {
        name: "RangeError",
        message: tooLong.store_error,
      });
    });
  }
});

describe("LocalDirectoryMediaStore on the file system", () => {
  test("putImage copies the bytes into private files and getImage reads them back", async () => {
    const parent = join(newDirectory(), "parent");
    const root = join(parent, "media");
    const store = new LocalDirectoryMediaStore(root);
    assert.equal(existsSync(root), false);

    const reference = await store.putImage(png);

    assert.equal(reference.uri, imageUri(store.storeUri, reference.sha256));
    assert.ok(reference.uri.startsWith("file://"));
    assert.deepEqual(new Uint8Array(readFileSync(store.imagePath(reference.sha256))), png);
    assert.deepEqual(imageFileNames(root), [reference.sha256]);
    assert.deepEqual(await store.getImage(reference), png);
    // A umask can only narrow these modes.
    assert.equal(modeOf(parent), 0o700);
    assert.equal(modeOf(root), 0o700);
    assert.equal(modeOf(join(root, "sha256")), 0o700);
    assert.equal(modeOf(store.imagePath(reference.sha256)), 0o600);
    await assert.rejects(store.putImage(new TextEncoder().encode("<svg/>")), { name: "ImageValidationError" });
    assert.deepEqual(imageFileNames(root), [reference.sha256]);
  });

  test("a repeated putImage keeps the existing file", async () => {
    const root = join(newDirectory(), "media");
    const first = await new LocalDirectoryMediaStore(root).putImage(png);
    const path = new LocalDirectoryMediaStore(root).imagePath(first.sha256);
    const inode = statSync(path).ino;

    const second = await new LocalDirectoryMediaStore(root).putImage(png);

    assert.deepEqual(second, first);
    assert.equal(statSync(path).ino, inode);
    assert.deepEqual(imageFileNames(root), [first.sha256]);
  });

  test("concurrent putImage calls with the same bytes leave one file", async () => {
    const root = join(newDirectory(), "media");
    const stores = [new LocalDirectoryMediaStore(root), new LocalDirectoryMediaStore(root)];

    const references = await Promise.all(
      Array.from({ length: 16 }, (_, index) => (stores[index % 2] as LocalDirectoryMediaStore).putImage(jpeg)),
    );

    assert.equal(new Set(references.map((reference) => reference.uri)).size, 1);
    const reference = references[0] as AgentImageReference;
    // No temporary file is left behind.
    assert.deepEqual(imageFileNames(root), [reference.sha256]);
    assert.deepEqual(await stores[0]?.getImage(reference), jpeg);
  });

  test("a tampered file is detected on read and replaced by putImage", async () => {
    const store = new LocalDirectoryMediaStore(join(newDirectory(), "media"));
    const reference = await store.putImage(png);
    const path = store.imagePath(reference.sha256);
    const mismatch = { name: "MediaStoreError", message: `image ${reference.uri} does not match its sha256 and size` };

    writeFileSync(path, Buffer.concat([png, Buffer.from([0])]));
    await assert.rejects(store.getImage(reference), mismatch);
    const flipped = Uint8Array.from(png);
    flipped[40] = (flipped[40] as number) ^ 0xff;
    writeFileSync(path, flipped);
    await assert.rejects(store.getImage(reference), mismatch);

    assert.deepEqual(await store.putImage(png), reference);
    assert.deepEqual(await store.getImage(reference), png);
  });

  test("a missing file or root is not in the media store", async () => {
    const directory = newDirectory();
    const store = new LocalDirectoryMediaStore(join(directory, "media"));
    await store.putImage(jpeg);
    const neverStored = buildImageReference(png, store.storeUri);
    const absentStore = new LocalDirectoryMediaStore(join(directory, "absent"));
    const absent = buildImageReference(png, absentStore.storeUri);

    await assert.rejects(store.getImage(neverStored), {
      name: "MediaStoreError",
      message: errors.missing.replace("{uri}", neverStored.uri),
    });
    await assert.rejects(absentStore.getImage(absent), {
      name: "MediaStoreError",
      message: errors.missing.replace("{uri}", absent.uri),
    });
  });

  test("a reference outside the root is refused", async () => {
    const directory = newDirectory();
    const store = new LocalDirectoryMediaStore(join(directory, "media"));
    const reference = await store.putImage(png);
    const elsewhere = new LocalDirectoryMediaStore(join(directory, "elsewhere"));
    await elsewhere.putImage(png);

    for (const uri of [
      imageUri(elsewhere.storeUri, reference.sha256),
      imageUri(`file://${directory}/media/..`, reference.sha256),
      imageUri(`s3://bucket${directory}/media`, reference.sha256),
    ]) {
      await assert.rejects(store.getImage(withUri(reference, uri)), {
        name: "MediaStoreError",
        message: `image ${uri} is not in the media store ${store.storeUri}`,
      });
    }
  });

  test("symbolic links under the root are refused", async () => {
    const directory = newDirectory();
    const outside = new LocalDirectoryMediaStore(join(directory, "outside"));
    const outsideReference = await outside.putImage(png);
    const store = new LocalDirectoryMediaStore(join(directory, "media"));
    await store.putImage(jpeg);
    const reference = withUri(outsideReference, imageUri(store.storeUri, outsideReference.sha256));
    const link = store.imagePath(reference.sha256);
    symlinkSync(outside.imagePath(reference.sha256), link);
    const notRegularFile = {
      name: "MediaStoreError",
      message: errors.not_regular_file.replace("{uri}", reference.uri),
    };

    await assert.rejects(store.getImage(reference), notRegularFile);
    await assert.rejects(store.putImage(png), notRegularFile);
    assert.ok(lstatSync(link).isSymbolicLink());

    const linkedRoot = join(directory, "linked-directory");
    mkdirSync(linkedRoot);
    symlinkSync(join(directory, "outside", "sha256"), join(linkedRoot, "sha256"));
    const linkedStore = new LocalDirectoryMediaStore(linkedRoot);
    const linkedReference = withUri(outsideReference, imageUri(linkedStore.storeUri, outsideReference.sha256));
    const notDirectory = {
      name: "MediaStoreError",
      message: errors.not_directory.replace("{path}", `${linkedRoot}/sha256`),
    };
    await assert.rejects(linkedStore.getImage(linkedReference), notDirectory);
    await assert.rejects(linkedStore.putImage(jpeg), notDirectory);
    assert.deepEqual(imageFileNames(join(directory, "outside")), [outsideReference.sha256]);
  });

  test("a special file under the root is refused without blocking", async () => {
    const store = new LocalDirectoryMediaStore(join(newDirectory(), "media"));
    await store.putImage(jpeg);
    const reference = buildImageReference(png, store.storeUri);
    execFileSync("mkfifo", [store.imagePath(reference.sha256)]);

    await assert.rejects(store.getImage(reference), {
      name: "MediaStoreError",
      message: errors.not_regular_file.replace("{uri}", reference.uri),
    });
  });

  test("the root itself may be a symbolic link", async () => {
    const directory = newDirectory();
    mkdirSync(join(directory, "target"));
    symlinkSync(join(directory, "target"), join(directory, "link"));
    const store = new LocalDirectoryMediaStore(join(directory, "link"));

    const reference = await store.putImage(png);

    assert.equal(store.root, join(directory, "link"));
    assert.deepEqual(imageFileNames(join(directory, "target")), [reference.sha256]);
    assert.deepEqual(await store.getImage(reference), png);
  });

  test("a file with another hard link is refused", async () => {
    const directory = newDirectory();
    const outside = join(directory, "outside.png");
    writeFileSync(outside, png);
    const store = new LocalDirectoryMediaStore(join(directory, "media"));
    await store.putImage(jpeg);
    const reference = buildImageReference(png, store.storeUri);
    linkSync(outside, store.imagePath(reference.sha256));
    const hardLink = { name: "MediaStoreError", message: errors.hard_link.replace("{uri}", reference.uri) };

    await assert.rejects(store.getImage(reference), hardLink);
    await assert.rejects(store.putImage(png), hardLink);
    assert.equal(statSync(outside).nlink, 2);
  });

  test("the first putImage syncs the parent of every directory it creates", async () => {
    const directory = newDirectory();
    const root = join(directory, "parent", "media");
    const store = new LocalDirectoryMediaStore(root);
    const synced = new Set<number>();

    const reference = await withFileHandleHook(
      "sync",
      async (handle) => {
        synced.add((await handle.stat()).ino);
      },
      () => store.putImage(png),
    );

    for (const created of [join(directory, "parent"), root, join(root, "sha256")]) {
      assert.ok(synced.has(statSync(join(created, "..")).ino), `the parent of ${created} was synced`);
    }
    // The sha256 directory after the rename, and the image file itself.
    assert.ok(synced.has(statSync(join(root, "sha256")).ino));
    assert.ok(synced.has(statSync(store.imagePath(reference.sha256)).ino));
  });

  test("every putImage syncs the directory chain another creator left unsynced", async () => {
    const directory = newDirectory();
    const root = join(directory, "media");
    // Another process created the root and never synced its parent.
    mkdirSync(root, { mode: 0o700 });
    const store = new LocalDirectoryMediaStore(root);
    const synced = new Set<number>();
    const recordSync = async (handle: FileHandle): Promise<void> => {
      synced.add((await handle.stat()).ino);
    };

    await withFileHandleHook("sync", recordSync, () => store.putImage(png));
    assert.ok(synced.has(statSync(directory).ino), "the root's parent was synced");
    assert.ok(synced.has(statSync(root).ino), "the root was synced");

    synced.clear();
    await withFileHandleHook("sync", recordSync, () => store.putImage(png));
    for (const path of [directory, root, join(root, "sha256")]) {
      assert.ok(synced.has(statSync(path).ino), `${path} was synced by the identical put`);
    }
  });

  test("a read whose directory is swapped after the open returns nothing", async () => {
    const directory = newDirectory();
    const root = join(directory, "media");
    const store = new LocalDirectoryMediaStore(root);
    const reference = await store.putImage(png);
    const outside = new LocalDirectoryMediaStore(join(directory, "outside"));
    await outside.putImage(png);
    let isArmed = true;

    await assert.rejects(
      withFileHandleHook(
        "stat",
        async () => {
          if (isArmed) {
            isArmed = false;
            swapImageDirectory(root, join(directory, "outside", "sha256"));
          }
        },
        () => store.getImage(reference),
      ),
      {
        name: "MediaStoreError",
        message: `media store directory ${root}/sha256 changed while image ${reference.uri} was read or written`,
      },
    );
  });

  test("a write whose directory is swapped before it lands is removed and refused", async () => {
    const directory = newDirectory();
    const root = join(directory, "media");
    const store = new LocalDirectoryMediaStore(root);
    const reference = await store.putImage(png);
    // A file with other bytes makes putImage write again after it checks the existing file.
    writeFileSync(store.imagePath(reference.sha256), Buffer.concat([png, Buffer.from([0])]));
    const outsideDirectory = join(directory, "outside");
    mkdirSync(outsideDirectory);
    let statCalls = 0;

    await assert.rejects(
      withFileHandleHook(
        "stat",
        async () => {
          // The first stat checks the opened file; the second compares its size, before the new write.
          statCalls += 1;
          if (statCalls === 2) {
            swapImageDirectory(root, outsideDirectory);
          }
        },
        () => store.putImage(png),
      ),
      {
        name: "MediaStoreError",
        message: `media store directory ${root}/sha256 changed while image ${reference.uri} was read or written`,
      },
    );
    assert.deepEqual(readdirSync(outsideDirectory), []);
  });
});
