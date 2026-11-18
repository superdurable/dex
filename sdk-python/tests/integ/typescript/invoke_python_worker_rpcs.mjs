// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Calls RPCs with and without input on a Python Worker through the TypeScript Client,
// and prints what they returned as JSON.
// Usage: node invoke_python_worker_rpcs.mjs <TypeScript SDK directory> <server address> <Flow ID>

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [sdkDirectory, serverAddress, flowId] = process.argv.slice(2);
const { Client, Registry, StepList, declareRPCMethod, openBlobCache, stringCodec } = await import(
  pathToFileURL(join(sdkDirectory, "dist", "src", "index.js")).href
);

// The Flow only types the Client calls; the Python Worker serves the RPCs.
class TypeScriptClientNoteFlow {
  getFlowType() {
    return "TypeScriptClientNoteFlow";
  }

  getSteps() {
    return StepList.empty();
  }

  getPersistenceSchema() {
    return {};
  }

  set_note(_context, _note) {
    throw new Error("the Python Worker serves set_note");
  }

  get_note(_context) {
    throw new Error("the Python Worker serves get_note");
  }

  clear_note(_context) {
    throw new Error("the Python Worker serves clear_note");
  }
}

declareRPCMethod(TypeScriptClientNoteFlow, "set_note", { inputCodec: stringCodec, outputCodec: stringCodec });
declareRPCMethod(TypeScriptClientNoteFlow, "get_note", { outputCodec: stringCodec });
declareRPCMethod(TypeScriptClientNoteFlow, "clear_note", {});

const flow = new TypeScriptClientNoteFlow();
const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-client-cache-"));
const cache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1024 * 1024 });
const client = new Client(new Registry([flow]), cache, { serverAddress });
try {
  const setNote = await client.invokeRPC(flow.set_note, flowId, "from TypeScript");
  const noteAfterSet = await client.invokeRPC(flow.get_note, flowId);
  const clearNote = await client.invokeRPC(flow.clear_note, flowId);
  const noteAfterClear = await client.invokeRPC(flow.get_note, flowId);
  console.log(JSON.stringify({ setNote, noteAfterSet, clearNote: clearNote ?? null, noteAfterClear }));
} finally {
  await client.close();
  cache.close();
  rmSync(cacheDirectory, { recursive: true, force: true });
}
