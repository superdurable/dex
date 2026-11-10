// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// S3MediaStore against a private s3local server. The Agents CI has no s3local binary yet, so these tests run only when
// DEX_AGENTS_S3LOCAL names one, built with `cargo build --release --bin s3local` in s3local/.

import assert from "node:assert/strict";
import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import {
  CreateBucketCommand,
  HeadObjectCommand,
  ListObjectsV2Command,
  PutObjectCommand,
  S3Client,
} from "@aws-sdk/client-s3";

import { ImageValidationError, MediaStoreError, imageUri } from "../../src/index.js";
import { S3MediaStore } from "../../src/s3/index.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";

const s3localBinary = process.env["DEX_AGENTS_S3LOCAL"];
const bucket = "agent-media";
const prefix = "agent-images";
const images = new Map(
  readConformanceJson<{
    readonly images: readonly {
      readonly name: string;
      readonly base64: string;
      readonly sha256: string;
      readonly size_bytes: number;
    }[];
  }>("images.json").images.map((image) => [image.name, image]),
);

async function findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close(() => resolve(typeof address === "object" && address !== null ? address.port : 0));
    });
  });
}

async function startS3local(binary: string, port: number): Promise<ChildProcess> {
  const child = spawn(
    binary,
    [
      "--listen-address",
      `127.0.0.1:${port}`,
      "--data-directory",
      join(mkdtempSync(join(tmpdir(), "s3local-")), "data"),
      "--durability",
      "process-crash-safe",
    ],
    { env: { ...process.env, RUST_LOG: "info" }, stdio: ["ignore", "pipe", "pipe"] },
  );
  await new Promise<void>((resolve, reject) => {
    let output = "";
    const onData = (chunk: Buffer): void => {
      output += chunk.toString();
      if (output.includes("s3local is serving")) {
        resolve();
      }
    };
    child.stdout?.on("data", onData);
    child.stderr?.on("data", onData);
    child.once("exit", (code) => reject(new Error(`s3local exited with ${code}: ${output}`)));
  });
  return child;
}

describe("S3MediaStore against s3local", { skip: s3localBinary === undefined && "set DEX_AGENTS_S3LOCAL" }, () => {
  let child: ChildProcess;
  let endpoint: string;
  let admin: S3Client;
  const stores: S3MediaStore[] = [];

  before(async () => {
    process.env["AWS_ACCESS_KEY_ID"] = "s3local";
    process.env["AWS_SECRET_ACCESS_KEY"] = "s3local";
    const port = await findFreePort();
    child = await startS3local(s3localBinary as string, port);
    endpoint = `http://127.0.0.1:${port}`;
    admin = new S3Client({ endpoint, region: "us-east-1", forcePathStyle: true });
    await admin.send(new CreateBucketCommand({ Bucket: bucket }));
  });

  after(() => {
    stores.forEach((store) => store.close());
    admin?.destroy();
    child?.kill("SIGKILL");
  });

  function storeAt(storePrefix: string): S3MediaStore {
    const store = new S3MediaStore({ bucket, prefix: storePrefix, endpoint, region: "us-east-1", forcePathStyle: true });
    stores.push(store);
    return store;
  }

  async function keys(keyPrefix: string): Promise<string[]> {
    const listing = await admin.send(new ListObjectsV2Command({ Bucket: bucket, Prefix: keyPrefix }));
    return (listing.Contents ?? []).map((entry) => entry.Key ?? "");
  }

  function bytesOf(name: string): Uint8Array {
    return new Uint8Array(Buffer.from(images.get(name)?.base64 ?? "", "base64"));
  }

  test("putImage writes one object per image with its content type", async () => {
    const store = storeAt(prefix);
    const png = bytesOf("png");

    const first = await store.putImage(png);
    const second = await store.putImage(png);

    assert.deepEqual(first, second);
    assert.equal(first.uri, `s3://${bucket}/${prefix}/sha256/${images.get("png")?.sha256}`);
    assert.deepEqual(await keys(prefix), [store.objectKey(first.sha256)]);
    const head = await admin.send(new HeadObjectCommand({ Bucket: bucket, Key: store.objectKey(first.sha256) }));
    assert.equal(head.ContentType, "image/png");
    assert.deepEqual(await store.getImage(first), png);
    const oversized = new Uint8Array(3_750_001);
    oversized.set(png.subarray(0, 8));
    await assert.rejects(store.putImage(oversized), ImageValidationError);
    await assert.rejects(store.putImage(new TextEncoder().encode("GIF00a")), ImageValidationError);
    assert.equal((await keys("")).length, 1);
  });

  test("getImage reads only its own prefix and exact bytes", async () => {
    const store = storeAt(prefix);
    const reference = await store.putImage(bytesOf("png"));
    const gif = images.get("gif");
    const jpeg = images.get("jpeg");
    assert.ok(gif !== undefined && jpeg !== undefined);
    await admin.send(new PutObjectCommand({ Bucket: bucket, Key: store.objectKey(jpeg.sha256), Body: "not the jpeg" }));

    await assert.rejects(storeAt("other").getImage(reference), {
      name: "MediaStoreError",
      message: new RegExp(`is not in the media store s3://${bucket}/other`),
    });
    await assert.rejects(
      store.getImage({
        ...reference,
        uri: imageUri(store.storeUri, gif.sha256),
        sha256: gif.sha256,
        mimeType: "image/gif",
        sizeBytes: gif.size_bytes,
      }),
      MediaStoreError,
    );
    await assert.rejects(
      store.getImage({
        ...reference,
        uri: imageUri(store.storeUri, jpeg.sha256),
        sha256: jpeg.sha256,
        mimeType: "image/jpeg",
        sizeBytes: jpeg.size_bytes,
      }),
      { name: "MediaStoreError", message: /does not match its sha256 and size/ },
    );
  });
});
