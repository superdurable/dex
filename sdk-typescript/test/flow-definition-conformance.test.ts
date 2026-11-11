// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { Server, ServerCredentials, type sendUnaryData } from "@grpc/grpc-js";

import { Registry, StepList, Worker, type BlobCache, type Flow } from "../src/index.js";
import {
  InvalidFlowDefinitionError,
  encodeJsonString,
  parseFlowDefinitionContent,
  parseRfc3339Milliseconds,
  prepareFlowDefinitions,
  rewriteSourcePath,
  shareableSourcePath,
  uploadWorkerFlowDefinitions,
  type PreparedFlowDefinition,
} from "../src/flow-definition-upload.js";
import {
  FlowServiceService,
  WorkerTaskServiceService,
  type FlowServiceServer,
  type ServerInfo,
  type SyncAttributeIndexResponse,
  type UploadFlowDefinitionsRequest,
  type UploadFlowDefinitionsResponse,
  type WorkerTaskServiceServer,
} from "../src/gen/dex.js";

interface SourcePathCase {
  readonly name: string;
  readonly cwd: string | null;
  readonly sourcePath: string;
  readonly expected: string | null;
}

interface Conformance {
  readonly documentValidity: readonly {
    readonly name: string;
    readonly document: string;
    readonly valid: boolean;
    readonly generatedAtError?: boolean;
  }[];
  readonly documentRewrite: readonly {
    readonly name: string;
    readonly cwd: string;
    readonly document: string;
    readonly expected: string | null;
  }[];
  readonly sourcePathRewrite: readonly SourcePathCase[];
  readonly jsonStringEncoding: readonly { readonly value: string; readonly encoded: string }[];
  readonly generatedAt: readonly { readonly value: string; readonly unixMillis?: number; readonly error?: boolean }[];
}

const conformance = JSON.parse(readFileSync(
  fileURLToPath(new URL("../../../protos/conformance/flow_definition_upload.json", import.meta.url)),
  "utf8",
)) as Conformance;

for (const testCase of conformance.sourcePathRewrite) {
  test(`source.path rewrite conformance: ${testCase.name}`, () => {
    assert.equal(shareableSourcePath(testCase.sourcePath, testCase.cwd ?? undefined), testCase.expected ?? undefined);
  });
}

test("JSON string encoding conformance", () => {
  for (const testCase of conformance.jsonStringEncoding) {
    assert.equal(encodeJsonString(testCase.value), testCase.encoded);
  }
});

test("generatedAt conformance", () => {
  for (const testCase of conformance.generatedAt) {
    if (testCase.error === true) {
      assert.throws(() => parseRfc3339Milliseconds(testCase.value), /not an RFC 3339 time/, testCase.value);
    } else {
      assert.equal(parseRfc3339Milliseconds(testCase.value).getTime(), testCase.unixMillis, testCase.value);
    }
  }
});

for (const testCase of conformance.documentValidity) {
  test(`document validity conformance: ${testCase.name}`, () => {
    const content = new TextEncoder().encode(testCase.document);
    if (testCase.valid) {
      parseFlowDefinitionContent(content, "test");
      return;
    }
    assert.throws(() => parseFlowDefinitionContent(content, "test"), (failure: unknown) => {
      // A generatedAt error is a configuration error, never a skippable non-FDG file.
      assert.equal(failure instanceof InvalidFlowDefinitionError, testCase.generatedAtError !== true);
      return true;
    });
  });
}

for (const testCase of conformance.documentRewrite) {
  test(`document rewrite conformance: ${testCase.name}`, () => {
    const content = new TextEncoder().encode(testCase.document);
    const rewritten = rewriteSourcePath(content, testCase.cwd);
    if (testCase.expected === null) {
      assert.equal(rewritten, content);
    } else {
      assert.equal(Buffer.from(rewritten).toString(), testCase.expected);
    }
  });
}

test("in-memory document with malformed generatedAt is rejected", () => {
  const content = fdg("MemoryFlow", "2.0", { generatedAt: "2026-11-01T00:00:00+00:99" });
  assert.throws(
    () => prepareFlowDefinitions([], [{ flowType: "MemoryFlow", content, generatedAt: new Date("2026-11-01T00:00:00Z") }]),
    /not RFC 3339/,
  );
});

function fdg(flowType: string, schemaVersion = "2.0", fields: Record<string, unknown> = {}): string {
  return JSON.stringify({ schemaVersion, flow: { name: flowType }, ...fields });
}

test("directory uploads only Flow Definition Graph 2.x documents", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "dex-flow-definition-directory-"));
  try {
    writeFileSync(path.join(directory, "package.json"), "{\"name\":\"app\",\"version\":\"1.0.0\"}");
    writeFileSync(path.join(directory, "old.json"), fdg("OldFlow", "1.0"));
    writeFileSync(path.join(directory, "broken.json"), "{not json");
    writeFileSync(path.join(directory, "notes.txt"), fdg("TextFlow"));
    mkdirSync(path.join(directory, "nested"));
    writeFileSync(path.join(directory, "nested", "Upper.JSON"), fdg("UpperFlow", "2.0", { generatedAt: "2026-11-01T00:00:00.9999Z" }));
    writeFileSync(path.join(directory, "valid.json"), fdg("ValidFlow"));
    const prepared = prepareFlowDefinitions([directory], []);
    assert.deepEqual(prepared.map((definition) => definition.flowType).sort(), ["UpperFlow", "ValidFlow"]);
    const upper = prepared.find((definition) => definition.flowType === "UpperFlow");
    assert.equal(upper?.generatedAt.toISOString(), "2026-11-01T00:00:00.999Z");

    writeFileSync(path.join(directory, "bad.json"), fdg("BadFlow", "2.0", { generatedAt: "2026-11-01 00:00:00Z" }));
    assert.throws(() => prepareFlowDefinitions([directory], []), /not RFC 3339/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("explicit files must be Flow Definition Graph 2.x documents", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "dex-flow-definition-explicit-"));
  try {
    const explicit = path.join(directory, "flow.data");
    writeFileSync(explicit, fdg("ExplicitFlow"));
    assert.deepEqual(prepareFlowDefinitions([explicit], []).map((definition) => definition.flowType), ["ExplicitFlow"]);
    const old = path.join(directory, "old.json");
    writeFileSync(old, fdg("OldFlow", "1.0"));
    assert.throws(() => prepareFlowDefinitions([old], []), /schemaVersion/);
    assert.throws(() => prepareFlowDefinitions([path.join(directory, "missing.json")], []), /does not exist/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("in-memory document must be Flow Definition Graph 2.x and has source.path rewritten", () => {
  const content = fdg("MemoryFlow", "2.0", { source: { path: "/elsewhere/a.json", language: "go" } });
  const [prepared] = prepareFlowDefinitions([], [{ flowType: "MemoryFlow", content, generatedAt: new Date("2026-11-01T00:00:00.123Z") }]);
  assert.equal(Buffer.from(prepared?.content ?? []).toString(), content.replace("\"/elsewhere/a.json\"", "\"a.json\""));
  assert.throws(
    () => prepareFlowDefinitions([], [{ flowType: "MemoryFlow", content: fdg("MemoryFlow", "1.0"), generatedAt: new Date() }]),
    /schemaVersion/,
  );
});

test("upload batches share one total deadline", async (t) => {
  let now = 1_000_000;
  t.mock.method(Date, "now", () => now);
  const definitions: PreparedFlowDefinition[] = Array.from({ length: 600 }, (_, index) => ({
    flowType: `Flow${index}`,
    content: new Uint8Array(2),
    generatedAt: new Date(0),
    origin: "test",
  }));
  const registered = new Set(definitions.map((definition) => definition.flowType));
  const deadlines: number[] = [];
  const upload = (stepMs: number) => async (_request: UploadFlowDefinitionsRequest, deadline: Date): Promise<UploadFlowDefinitionsResponse> => {
    deadlines.push(deadline.getTime());
    now += stepMs;
    return { results: [] };
  };
  const originalWarn = console.warn;
  console.warn = () => {};
  try {
    await uploadWorkerFlowDefinitions(upload(4_000), definitions, registered, "test", 10_000);
    assert.deepEqual(deadlines, [1_010_000, 1_010_000, 1_010_000]);
    deadlines.length = 0;
    now = 1_000_000;
    await uploadWorkerFlowDefinitions(upload(11_000), definitions, registered, "test", 10_000);
    assert.deepEqual(deadlines, [1_010_000]);
  } finally {
    console.warn = originalWarn;
  }
});

class HangingUploadFlow implements Flow {
  public getFlowType(): string {
    return "HangingUploadFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.empty();
  }
}

class MemoryBlobCache implements BlobCache {
  public readonly config = { directory: "memory", maxBytes: 1_024 };

  public get(_blobId: string): Uint8Array | undefined {
    return undefined;
  }

  public put(_blobId: string, _payload: Uint8Array): boolean {
    return true;
  }

  public delete(_blobId: string): void {}

  public deleteAll(): void {}

  public close(): void {}
}

test("Worker close cancels an in-flight upload", async () => {
  let resolveUploadStarted!: () => void;
  const uploadStarted = new Promise<void>((resolve) => {
    resolveUploadStarted = resolve;
  });
  const server = new Server();
  server.addService(WorkerTaskServiceService, {
    pollWorkerTask(call) {
      call.on("end", () => call.end());
    },
  } as WorkerTaskServiceServer);
  server.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      callback(null, { serverVersion: "test", minimumSupportedProtocolVersion: 1, currentProtocolVersion: 4, serverEngine: 0 });
    },
    syncAttributeIndexes(_call, callback: sendUnaryData<SyncAttributeIndexResponse>) {
      callback(null, {});
    },
    uploadFlowDefinitions() {
      resolveUploadStarted();
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (error, boundPort) => {
      if (error !== null) reject(error);
      else resolve(boundPort);
    });
  });
  const worker = new Worker(new Registry([new HangingUploadFlow()]), new MemoryBlobCache(), {
    serverAddress: `127.0.0.1:${port}`,
    flowDefinitions: [{ flowType: "HangingUploadFlow", content: fdg("HangingUploadFlow"), generatedAt: new Date() }],
  });
  const originalWarn = console.warn;
  console.warn = () => {};
  try {
    const start = worker.start();
    await uploadStarted;
    const closeStartedAt = Date.now();
    await worker.close();
    await assert.rejects(start, /stopped during startup/);
    assert.ok(Date.now() - closeStartedAt < 5_000);
  } finally {
    console.warn = originalWarn;
    server.forceShutdown();
  }
});
