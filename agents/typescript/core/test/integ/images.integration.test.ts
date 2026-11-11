// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The shared image cases in agents/conformance/images.json, and the in-memory store.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";

import {
  ToolExecutionError,
  ImageValidationError,
  InMemoryMediaStore,
  MediaStoreError,
  buildImageReference,
  collectTurnImages,
  detectImageMimeType,
  imageUri,
  loadImages,
  maxImageBytes,
  requireShortImageUris,
  maxImageUriLength,
  maxImagesPerMessage,
  supportedImageMimeTypes,
  validateImageReference,
  validateImageReferences,
  type AgentImageReference,
  type AgentMessage,
  type AgentToolCall,
  type AgentTurnContext,
} from "../../src/index.js";
import { decodeAgentImageReference, encodeAgentImageReference } from "../../src/agent-session-codecs.js";
import { storeToolResultImages } from "../../src/mcp/mcp-image-content.js";
import { outcomeFromToolResult } from "../../src/mcp/mcp-tool-source.js";
import { S3MediaStore } from "../../src/s3/index.js";
import { readConformanceJson, serializeCanonicalJson } from "../fixtures/conformance-suite.js";

interface ImageCase {
  readonly name: string;
  readonly base64: string;
  readonly mime_type: string;
  readonly sha256: string;
  readonly size_bytes: number;
  readonly reference: string;
}

interface ImageSuite {
  readonly limits: {
    readonly max_image_bytes: number;
    readonly max_images_per_message: number;
    readonly max_image_uri_length: number;
    readonly supported_mime_types: readonly string[];
  };
  readonly images: readonly ImageCase[];
  readonly padded_images: readonly {
    readonly name: string;
    readonly prefix_base64: string;
    readonly size_bytes: number;
    readonly mime_type: string | null;
    readonly error: string | null;
  }[];
  readonly rejected_images: readonly { readonly name: string; readonly base64: string; readonly error: string }[];
  readonly verify_cases: readonly {
    readonly name: string;
    readonly image: string;
    readonly reference_mime_type: string;
    readonly error: string;
  }[];
  readonly store_locations: {
    readonly sha256: string;
    readonly s3: readonly {
      readonly name: string;
      readonly bucket: string;
      readonly prefix: string;
      readonly store_uri: string;
      readonly object_key: string;
      readonly image_uri: string;
    }[];
    readonly s3_store_uris: readonly { readonly store_uri: string; readonly bucket: string; readonly prefix: string }[];
    readonly memory: readonly { readonly name: string; readonly store_uri: string; readonly image_uri: string }[];
    readonly too_long: readonly {
      readonly name: string;
      readonly store_uri: string;
      readonly store_error: string;
      readonly build_error: string;
    }[];
  };
  readonly references: {
    readonly valid: readonly { readonly name: string; readonly wire: string }[];
    readonly invalid: readonly { readonly name: string; readonly wire: string; readonly error: string }[];
  };
  readonly reference_lists: readonly {
    readonly name: string;
    readonly wires: readonly string[];
    readonly repeat: number;
    readonly error: string | null;
  }[];
  readonly mcp_image_content: {
    readonly tool_name: string;
    readonly cases: readonly {
      readonly name: string;
      readonly content: CallToolResult["content"];
      readonly expected?: {
        readonly output: string;
        readonly images: readonly { readonly sha256: string; readonly mime_type: string; readonly size_bytes: number }[];
      };
      readonly expected_error?: string;
    }[];
    readonly failure_cases: readonly {
      readonly name: string;
      readonly result: CallToolResult;
      readonly expected: { readonly kind: string; readonly message: string };
    }[];
  };
}

const suite = readConformanceJson<ImageSuite>("images.json");

function bytesOf(base64: string): Uint8Array {
  return new Uint8Array(Buffer.from(base64, "base64"));
}

function decodeReference(wire: string): AgentImageReference {
  return decodeAgentImageReference(JSON.parse(wire));
}

function imageOf(name: string): ImageCase {
  const found = suite.images.find((image) => image.name === name);
  assert.ok(found, `images.json has no ${name}`);
  return found;
}

describe("image conformance cases", () => {
  test("the limits match the core constants", () => {
    assert.equal(suite.limits.max_image_bytes, maxImageBytes);
    assert.equal(suite.limits.max_images_per_message, maxImagesPerMessage);
    assert.equal(suite.limits.max_image_uri_length, maxImageUriLength);
    assert.deepEqual(suite.limits.supported_mime_types, supportedImageMimeTypes);
  });

  for (const image of suite.images) {
    test(`${image.name}: type, hash, size, and canonical reference`, () => {
      const data = bytesOf(image.base64);
      const reference = buildImageReference(data, "memory://conformance");
      assert.equal(detectImageMimeType(data), image.mime_type);
      assert.deepEqual([reference.mimeType, reference.sha256, reference.sizeBytes], [
        image.mime_type,
        image.sha256,
        image.size_bytes,
      ]);
      assert.equal(serializeCanonicalJson(encodeAgentImageReference(reference)), image.reference);
    });
  }

  for (const padded of suite.padded_images) {
    test(`padded images are checked for size, then structure: ${padded.name}`, () => {
      const prefix = bytesOf(padded.prefix_base64);
      const data = new Uint8Array(padded.size_bytes);
      data.set(prefix);
      if (padded.error === null) {
        assert.equal(buildImageReference(data, "memory://conformance").mimeType, padded.mime_type);
      } else {
        assert.throws(() => buildImageReference(data, "memory://conformance"), {
          name: "ImageValidationError",
          message: padded.error,
        });
      }
    });
  }

  for (const rejected of suite.rejected_images) {
    test(`refuses bytes that are not a supported image: ${rejected.name}`, () => {
      assert.throws(() => buildImageReference(bytesOf(rejected.base64), "memory://conformance"), {
        name: "ImageValidationError",
        message: rejected.error,
      });
    });
  }

  for (const location of suite.store_locations.s3) {
    test(`an S3 store names objects by content: ${location.name}`, () => {
      const store = new S3MediaStore({ bucket: location.bucket, prefix: location.prefix, region: "us-east-1" });
      try {
        assert.equal(store.storeUri, location.store_uri);
        assert.equal(store.objectKey(suite.store_locations.sha256), location.object_key);
        assert.equal(imageUri(store.storeUri, suite.store_locations.sha256), location.image_uri);
        assert.equal(location.image_uri, `s3://${location.bucket}/${location.object_key}`);
      } finally {
        store.close();
      }
    });
  }

  for (const location of suite.store_locations.s3_store_uris) {
    test(`an S3 store URI names a bucket and prefix: ${location.store_uri}`, () => {
      const store = S3MediaStore.fromStoreUri(location.store_uri, { region: "us-east-1" });
      const expected = new S3MediaStore({ bucket: location.bucket, prefix: location.prefix, region: "us-east-1" });
      try {
        assert.equal(store.storeUri, expected.storeUri);
      } finally {
        store.close();
        expected.close();
      }
    });
  }

  for (const location of suite.store_locations.memory) {
    test(`an in-memory store names images under its name: ${location.name}`, () => {
      const store = new InMemoryMediaStore(location.name);
      assert.equal(store.storeUri, location.store_uri);
      assert.equal(imageUri(store.storeUri, suite.store_locations.sha256), location.image_uri);
    });
  }

  for (const verify of suite.verify_cases) {
    test(`a reference whose type was changed fails on read: ${verify.name}`, async () => {
      const store = new InMemoryMediaStore("conformance");
      const reference = await store.putImage(bytesOf(imageOf(verify.image).base64));
      const relabeled = { ...reference, mimeType: verify.reference_mime_type };

      validateImageReference(relabeled);
      await assert.rejects(store.getImage(relabeled), {
        name: "MediaStoreError",
        message: verify.error.replace("{uri}", reference.uri),
      });
    });
  }

  for (const tooLong of suite.store_locations.too_long) {
    test(`a store location too long for its image URIs is refused: ${tooLong.name}`, () => {
      assert.throws(() => requireShortImageUris(tooLong.store_uri), {
        name: "RangeError",
        message: tooLong.store_error,
      });
      const name = tooLong.store_uri.slice("memory://".length);
      assert.throws(() => new InMemoryMediaStore(name), RangeError);
      assert.throws(() => new S3MediaStore({ bucket: "agent-media", prefix: name, region: "us-east-1" }), RangeError);
      assert.throws(() => buildImageReference(bytesOf(imageOf("png").base64), tooLong.store_uri), {
        name: "ImageValidationError",
        message: tooLong.build_error,
      });
    });
  }

  for (const valid of suite.references.valid) {
    test(`a valid reference passes: ${valid.name}`, () => {
      validateImageReference(decodeReference(valid.wire));
    });
  }

  for (const invalid of suite.references.invalid) {
    test(`an invalid reference fails with the shared text: ${invalid.name}`, () => {
      assert.throws(() => validateImageReference(decodeReference(invalid.wire)), {
        name: "ImageValidationError",
        message: invalid.error,
      });
    });
  }

  for (const list of suite.reference_lists) {
    test(`reference lists are bounded and name the bad index: ${list.name}`, () => {
      const once = list.wires.map(decodeReference);
      const references = Array.from({ length: list.repeat }, () => once).flat();
      if (list.error === null) {
        validateImageReferences(references);
      } else {
        assert.throws(() => validateImageReferences(references), {
          name: "ImageValidationError",
          message: list.error,
        });
      }
    });
  }
});

describe("the in-memory media store", () => {
  test("keeps one object per image and reads it back", async () => {
    const store = new InMemoryMediaStore("conformance");
    const png = bytesOf(imageOf("png").base64);

    const first = await store.putImage(png);
    const second = await store.putImage(png);

    assert.deepEqual(first, second);
    assert.equal(store.imageCount, 1);
    assert.deepEqual(await store.getImage(first), png);
    const oversized = new Uint8Array(maxImageBytes + 1);
    oversized.set(bytesOf(suite.padded_images[0]?.prefix_base64 ?? ""));
    await assert.rejects(store.putImage(oversized), { name: "ImageValidationError", message: /the limit is 3750000/ });
    await assert.rejects(store.putImage(new TextEncoder().encode("<svg/>")), ImageValidationError);
    assert.equal(store.imageCount, 1);
  });

  test("returns a copy, so changing it leaves the stored image intact", async () => {
    const store = new InMemoryMediaStore("conformance");
    const png = bytesOf(imageOf("png").base64);
    const reference = await store.putImage(png);

    const first = await store.getImage(reference);
    first.fill(0);

    assert.deepEqual(await store.getImage(reference), png);
  });

  test("reads only its own images with their exact bytes", async () => {
    const store = new InMemoryMediaStore("conformance");
    const reference = await store.putImage(bytesOf(imageOf("png").base64));
    const missing = buildImageReference(bytesOf(imageOf("jpeg").base64), store.storeUri);

    await assert.rejects(store.getImage({ ...reference, uri: imageUri("memory://elsewhere", reference.sha256) }), {
      name: "MediaStoreError",
      message: /is not in the media store memory:\/\/conformance/,
    });
    await assert.rejects(store.getImage(missing), MediaStoreError);
    await assert.rejects(store.getImage({ ...reference, mimeType: "image/bmp" }), {
      name: "ImageValidationError",
      message: /mime_type/,
    });
  });

  test("loadImages reads each distinct image once", async () => {
    const reads: string[] = [];
    class CountingStore extends InMemoryMediaStore {
      public override async getImage(reference: AgentImageReference): Promise<Uint8Array> {
        reads.push(reference.sha256);
        return super.getImage(reference);
      }
    }
    const store = new CountingStore();
    const png = await store.putImage(bytesOf(imageOf("png").base64));
    const gif = await store.putImage(bytesOf(imageOf("gif").base64));

    const loaded = await loadImages(store, [png, gif, png]);

    assert.deepEqual([...reads].sort(), [png.sha256, gif.sha256].sort());
    assert.deepEqual(loaded.get(png.sha256), bytesOf(imageOf("png").base64));
    assert.deepEqual(loaded.get(gif.sha256), bytesOf(imageOf("gif").base64));
    assert.equal(loaded.size, 2);
  });

  test("collectTurnImages takes messages, then records in deferral order, each image once", () => {
    const [png, jpeg, gif, webp] = ["png", "jpeg", "gif", "webp"].map((name) =>
      buildImageReference(bytesOf(imageOf(name).base64), "memory://conformance"),
    ) as [AgentImageReference, AgentImageReference, AgentImageReference, AgentImageReference];
    const message = (sequence: number, images: AgentImageReference[]): AgentMessage => ({
      sequence,
      turnId: "turn-1",
      role: "user",
      text: null,
      toolCallId: null,
      toolName: null,
      toolArguments: null,
      toolOutput: null,
      frameworkItem: "{}",
      createdAt: "2026-11-10T00:00:00.000Z",
      images,
    });
    const record = (toolCallId: string, deferralOrder: number, images: AgentImageReference[]): AgentToolCall => ({
      toolCallId,
      turnId: "turn-1",
      toolName: "take_screenshot",
      arguments: "{}",
      effect: "read",
      requiresApproval: false,
      status: "succeeded",
      output: "ok",
      error: null,
      rejectionMessage: null,
      deferralOrder,
      images,
    });
    const turn = {
      messages: [message(1, [png, jpeg]), message(2, [png])],
      toolCalls: [record("b", 1, [webp]), record("a", 0, [gif, jpeg])],
    } as unknown as AgentTurnContext;

    assert.deepEqual(collectTurnImages(turn), [png, jpeg, gif, webp]);
  });
});

describe("MCP image content conformance cases", () => {
  const toolName = suite.mcp_image_content.tool_name;
  const knownBytes = new Map(suite.images.map((image) => [image.sha256, bytesOf(image.base64)]));
  for (const testCase of suite.mcp_image_content.cases) {
    test(testCase.name, async () => {
      const store = new InMemoryMediaStore("mcp-conformance");
      const result: CallToolResult = { content: [...testCase.content] };
      if (testCase.expected_error !== undefined) {
        await assert.rejects(storeToolResultImages(result, store, toolName), (error: unknown) => {
          assert.ok(error instanceof ToolExecutionError);
          assert.equal(error.message, testCase.expected_error);
          return true;
        });
        return;
      }
      const converted = await storeToolResultImages(result, store, toolName);
      assert.equal(converted.output, testCase.expected?.output);
      const images = converted.images ?? [];
      assert.deepEqual(
        images.map((image) => ({ sha256: image.sha256, mime_type: image.mimeType, size_bytes: image.sizeBytes })),
        testCase.expected?.images,
      );
      for (const image of images) {
        assert.deepEqual(await store.getImage(image), knownBytes.get(image.sha256));
      }
    });
  }

  for (const testCase of suite.mcp_image_content.failure_cases) {
    test(`a failed result: ${testCase.name}`, () => {
      const outcome = outcomeFromToolResult(testCase.result);
      assert.ok(outcome.kind === "failed" || outcome.kind === "outcome_unknown");
      assert.deepEqual({ kind: outcome.kind, message: outcome.message }, testCase.expected);
    });
  }
});
