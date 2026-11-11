// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// InMemoryMediaStore cannot cross the Worker process boundary, so a test with a Worker child keeps image bytes here:
// one file per distinct image, named by its SHA-256.

import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import {
  MediaStoreError,
  buildImageReference,
  requireStoreImageReference,
  verifyImageBytes,
  type AgentImageReference,
  type MediaStore,
} from "../../src/index.js";

export const directoryMediaStoreUri = "directory://test-media";

export class DirectoryMediaStore implements MediaStore {
  public readonly storeUri = directoryMediaStoreUri;

  public constructor(private readonly directory: string) {}

  public imageFileNames(): string[] {
    return existsSync(this.directory)
      ? readdirSync(this.directory)
          .filter((name) => !name.startsWith("."))
          .sort()
      : [];
  }

  public async putImage(data: Uint8Array): Promise<AgentImageReference> {
    const reference = buildImageReference(data, this.storeUri);
    mkdirSync(this.directory, { recursive: true });
    const path = join(this.directory, reference.sha256);
    if (!existsSync(path)) {
      // A rename is atomic, so a reader never sees a partial image.
      const staging = join(this.directory, `.${reference.sha256}.${process.pid}`);
      writeFileSync(staging, data);
      renameSync(staging, path);
    }
    return reference;
  }

  public async getImage(reference: AgentImageReference): Promise<Uint8Array> {
    requireStoreImageReference(reference, this.storeUri);
    const path = join(this.directory, reference.sha256);
    if (!existsSync(path)) {
      throw new MediaStoreError(`image ${reference.uri} is not in the media store`);
    }
    return verifyImageBytes(reference, new Uint8Array(readFileSync(path)));
  }
}
