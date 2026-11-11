// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// S3MediaStore's conditional upload against a scripted S3 client: 412, 409, and the fallback.

import assert from "node:assert/strict";
import { afterEach, describe, mock, test } from "node:test";

import { HeadObjectCommand, PutObjectCommand, S3Client, S3ServiceException } from "@aws-sdk/client-s3";

import { S3MediaStore } from "../../src/s3/index.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";

const pngCase = readConformanceJson<{
  readonly images: readonly { readonly name: string; readonly base64: string; readonly sha256: string }[];
}>("images.json").images.find((image) => image.name === "png");
const png = new Uint8Array(Buffer.from(pngCase?.base64 ?? "", "base64"));

function s3Error(status: number, name: string): S3ServiceException {
  return new S3ServiceException({ name, $fault: "client", $metadata: { httpStatusCode: status }, message: name });
}

/** Answers each PutObject with the next scripted error, or success once the script ends. */
class ScriptedS3 {
  public readonly puts: { readonly isConditional: boolean }[] = [];
  public heads = 0;

  public constructor(
    private readonly putErrors: S3ServiceException[],
    public storedSize: number | undefined,
  ) {}

  public async send(command: unknown): Promise<unknown> {
    if (command instanceof PutObjectCommand) {
      this.puts.push({ isConditional: command.input.IfNoneMatch === "*" });
      const error = this.putErrors.shift();
      if (error !== undefined) {
        throw error;
      }
      this.storedSize = (command.input.Body as Uint8Array).length;
      return {};
    }
    if (command instanceof HeadObjectCommand) {
      this.heads += 1;
      if (this.storedSize === undefined) {
        throw s3Error(404, "NotFound");
      }
      return { ContentLength: this.storedSize };
    }
    throw new Error(`unexpected command ${String(command)}`);
  }
}

function useScriptedS3(scripted: ScriptedS3): S3MediaStore {
  mock.method(S3Client.prototype, "send", (command: unknown) => scripted.send(command));
  return new S3MediaStore({
    bucket: "agent-media",
    prefix: "images",
    region: "us-east-1",
    credentials: { accessKeyId: "a", secretAccessKey: "b" },
  });
}

describe("S3MediaStore conditional uploads", () => {
  afterEach(() => {
    mock.restoreAll();
  });

  test("an existing object answers 412 and needs no HEAD", async () => {
    const scripted = new ScriptedS3([s3Error(412, "PreconditionFailed")], png.length);

    const reference = await useScriptedS3(scripted).putImage(png);

    assert.equal(reference.sha256, pngCase?.sha256);
    assert.deepEqual(scripted.puts, [{ isConditional: true }]);
    assert.equal(scripted.heads, 0);
  });

  test("a 409 conflict retries the conditional upload", async () => {
    const scripted = new ScriptedS3([s3Error(409, "ConditionalRequestConflict")], undefined);

    const reference = await useScriptedS3(scripted).putImage(png);

    assert.equal(reference.sizeBytes, png.length);
    assert.deepEqual(scripted.puts, [{ isConditional: true }, { isConditional: true }]);
    assert.equal(scripted.storedSize, png.length);
  });

  test("conflicts on every attempt with the object missing fail the upload", async () => {
    const conflict = (): S3ServiceException => s3Error(409, "ConditionalRequestConflict");
    const scripted = new ScriptedS3([conflict(), conflict(), conflict()], undefined);

    await assert.rejects(useScriptedS3(scripted).putImage(png), {
      name: "MediaStoreError",
      message: /was not stored: every upload conflicted/,
    });
    assert.equal(scripted.puts.length, 3);
    assert.equal(scripted.heads, 1);
  });

  test("conflicts on every attempt succeed when the object is there", async () => {
    const conflict = (): S3ServiceException => s3Error(409, "ConditionalRequestConflict");
    const scripted = new ScriptedS3([conflict(), conflict(), conflict()], png.length);

    const reference = await useScriptedS3(scripted).putImage(png);

    assert.equal(reference.sha256, pngCase?.sha256);
    assert.deepEqual([scripted.puts.length, scripted.heads], [3, 1]);
  });

  test("conflicts with an object of another size fail the upload", async () => {
    const conflict = (): S3ServiceException => s3Error(409, "ConditionalRequestConflict");
    const scripted = new ScriptedS3([conflict(), conflict(), conflict()], png.length + 1);

    await assert.rejects(useScriptedS3(scripted).putImage(png), { name: "MediaStoreError", message: /was not stored/ });
  });

  test("a service without conditional writes falls back to HEAD, then PUT", async () => {
    const scripted = new ScriptedS3([s3Error(501, "NotImplemented")], undefined);
    const store = useScriptedS3(scripted);

    await store.putImage(png);
    await store.putImage(png);

    assert.deepEqual(scripted.puts, [{ isConditional: true }, { isConditional: false }]);
    assert.equal(scripted.heads, 2);
  });
});
