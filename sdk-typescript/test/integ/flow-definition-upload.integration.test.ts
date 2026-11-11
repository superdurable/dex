// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import { credentials } from "@grpc/grpc-js";

import {
  Client,
  FlowDefinitionUploadOutcome,
  Registry,
  Worker,
  doubleCodec,
  openBlobCache,
  type BlobCache,
  type WorkerOptions,
} from "../../src/index.js";
import { FlowServiceClient, type FlowDefinition } from "../../src/gen/dex.js";
import { BasicFlow } from "./basic_flow.js";

const serverAddress = process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801";
const generatedAt = new Date("2026-11-01T12:30:00Z");

class UploadedBasicFlow extends BasicFlow {
  public constructor(private readonly flowType: string) {
    super();
  }

  public override getFlowType(): string {
    return this.flowType;
  }
}

test("Worker uploads registered definitions from paths and memory", async () => {
  const fromFile = uniqueFlowType("TsFileFlow");
  const fromMemory = uniqueFlowType("TsMemoryFlow");
  const unregistered = uniqueFlowType("TsUnregisteredFlow");
  const directory = mkdtempSync(path.join(process.cwd(), "flow-definition-test-"));
  try {
    mkdirSync(path.join(directory, "nested"));
    const absoluteSource = path.join(directory, "workflow.ts");
    const fileContent = flowDefinitionJson(fromFile, absoluteSource, "2026-10-01T08:00:00Z");
    writeFileSync(path.join(directory, "nested", "file-flow.json"), fileContent);
    writeFileSync(path.join(directory, "nested", "ignored.txt"), "not a definition");
    writeFileSync(path.join(directory, "unregistered.json"), flowDefinitionJson(unregistered, "workflow.ts"));
    const memoryContent = flowDefinitionJson(fromMemory, "/elsewhere/agent/session.ts");
    const flows = [new UploadedBasicFlow(fromFile), new UploadedBasicFlow(fromMemory)];
    await withWorker(flows, {
      flowDefinitionPaths: [directory],
      flowDefinitions: [{ flowType: fromMemory, content: memoryContent, generatedAt }],
    }, async () => {});
    const stored = await getFlowDefinitions([fromFile, fromMemory, unregistered]);
    assert.deepEqual([...stored.keys()].sort(), [fromFile, fromMemory].sort());

    const relativeSource = path.relative(process.cwd(), absoluteSource).split(path.sep).join("/");
    const storedFile = stored.get(fromFile);
    assert.equal(
      Buffer.from(storedFile?.content ?? []).toString(),
      fileContent.replace(JSON.stringify(absoluteSource), JSON.stringify(relativeSource)),
    );
    assert.equal(storedFile?.info?.generatedAt?.toISOString(), "2026-10-01T08:00:00.000Z");
    assert.match(storedFile?.info?.uploader ?? "", /^sdk-typescript\//);

    const storedMemory = stored.get(fromMemory);
    assert.equal(
      Buffer.from(storedMemory?.content ?? []).toString(),
      memoryContent.replace("\"/elsewhere/agent/session.ts\"", "\"session.ts\""),
    );
    assert.equal(storedMemory?.info?.generatedAt?.toISOString(), generatedAt.toISOString());
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("rejected upload does not stop the Worker", async () => {
  const flowType = uniqueFlowType("TsRejectedFlow");
  const rejected = flowDefinitionJson(flowType, "workflow.ts");
  const tooFarAhead = new Date(Date.now() + 3 * 86_400_000);
  const flow = new UploadedBasicFlow(flowType);
  const warnings: string[] = [];
  const originalWarn = console.warn;
  console.warn = (...values: unknown[]) => {
    warnings.push(values.map(String).join(" "));
  };
  try {
    await withWorker([flow], { flowDefinitions: [{ flowType, content: rejected, generatedAt: tooFarAhead }] }, async (client) => {
      const id = `rejected-definition-${randomUUID()}`;
      await client.startFlow(flow, id, 10);
      assert.equal((await client.waitForFlow(id)).singleOutput(doubleCodec), 12);
    });
  } finally {
    console.warn = originalWarn;
  }
  assert.equal((await getFlowDefinitions([flowType])).size, 0);
  assert.ok(warnings.some((warning) => warning.includes("Flow definition upload failed")), warnings.join("\n"));
});

test("duplicate Flow type across sources fails Worker construction", async () => {
  const flowType = uniqueFlowType("TsDuplicateFlow");
  const directory = mkdtempSync(path.join(tmpdir(), "dex-flow-definition-duplicate-"));
  const { blobCache, close } = openTestBlobCache();
  try {
    const file = path.join(directory, "flow.json");
    writeFileSync(file, flowDefinitionJson(flowType, "workflow.ts"));
    assert.throws(
      () => new Worker(new Registry([new UploadedBasicFlow(flowType)]), blobCache, {
        serverAddress,
        flowDefinitionPaths: [file],
        flowDefinitions: [{ flowType, content: flowDefinitionJson(flowType, "x.ts"), generatedAt }],
      }),
      /configured twice/,
    );
  } finally {
    close();
    rmSync(directory, { recursive: true, force: true });
  }
});

test("Client upload reports outcomes and force replaces", async () => {
  const flowType = uniqueFlowType("TsClientFlow");
  const first = flowDefinitionJson(flowType, "first.ts");
  const second = flowDefinitionJson(flowType, "second.ts");
  const older = new Date(generatedAt.getTime() - 86_400_000);
  const { blobCache, close } = openTestBlobCache();
  const client = new Client(new Registry([new UploadedBasicFlow(flowType)]), blobCache, { serverAddress });
  try {
    const created = await client.uploadFlowDefinitions([{ flowType, content: first, generatedAt }]);
    const unchanged = await client.uploadFlowDefinitions([{ flowType, content: first, generatedAt }]);
    const kept = await client.uploadFlowDefinitions([{ flowType, content: second, generatedAt: older }]);
    const forced = await client.uploadFlowDefinitions([{ flowType, content: second, generatedAt: older }], true);
    assert.deepEqual(
      [...created, ...unchanged, ...kept, ...forced].map((result) => result.outcome),
      [
        FlowDefinitionUploadOutcome.CREATED,
        FlowDefinitionUploadOutcome.UNCHANGED,
        FlowDefinitionUploadOutcome.KEPT_EXISTING,
        FlowDefinitionUploadOutcome.REPLACED,
      ],
    );
    assert.equal(kept[0]?.storedGeneratedAt.toISOString(), generatedAt.toISOString());
    assert.equal(forced[0]?.storedGeneratedAt.toISOString(), older.toISOString());
    const stored = await getFlowDefinitions([flowType]);
    assert.equal(Buffer.from(stored.get(flowType)?.content ?? []).toString(), second);
  } finally {
    await client.close();
    close();
  }
});

function uniqueFlowType(prefix: string): string {
  return `${prefix}${randomUUID().replaceAll("-", "")}`;
}

function flowDefinitionJson(flowType: string, sourcePath: string, documentGeneratedAt?: string): string {
  return JSON.stringify({
    schemaVersion: "2.0",
    valid: true,
    ...(documentGeneratedAt === undefined ? {} : { generatedAt: documentGeneratedAt }),
    source: { language: "typescript", path: sourcePath },
    flow: { name: flowType, startStepId: "step:Start" },
    nodes: [],
    edges: [],
  }, null, 2);
}

function openTestBlobCache(): { readonly blobCache: BlobCache; close(): void } {
  const directory = mkdtempSync(path.join(tmpdir(), "dex-flow-definition-cache-"));
  const blobCache = openBlobCache({ directory, maxBytes: 64 * 1_024 * 1_024 });
  return {
    blobCache,
    close() {
      blobCache.close();
      rmSync(directory, { recursive: true, force: true });
    },
  };
}

async function withWorker(
  flows: readonly UploadedBasicFlow[],
  options: WorkerOptions,
  run: (client: Client) => Promise<void>,
): Promise<void> {
  const registry = new Registry(flows);
  const { blobCache, close } = openTestBlobCache();
  const worker = new Worker(registry, blobCache, { ...options, serverAddress });
  const client = new Client(registry, blobCache, { serverAddress });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    await run(client);
  } finally {
    await client.close();
    await worker.close();
    close();
  }
}

async function getFlowDefinitions(flowTypes: readonly string[]): Promise<Map<string, FlowDefinition>> {
  const service = new FlowServiceClient(serverAddress, credentials.createInsecure());
  try {
    const response = await new Promise<FlowDefinition[]>((resolve, reject) => {
      service.getFlowDefinitions({ flowTypes: [...flowTypes] }, (error, result) => {
        if (error !== null) {
          reject(error);
          return;
        }
        resolve(result.definitions);
      });
    });
    return new Map(response.map((definition) => [definition.info?.flowType ?? "", definition]));
  } finally {
    service.close();
  }
}
