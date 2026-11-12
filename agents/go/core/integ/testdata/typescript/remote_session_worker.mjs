// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Serves a built-in loop agent with every session feature, for the Go remote session tests. It is
// the TypeScript twin of ../python/remote_session_worker.py and reads the same commands:
// - `say <text>` answers `said: <text>`.
// - `publish <message>` calls `publish_note`, a write that needs approval.
// - `ask <question> | <choice> ...` calls `ask_user` with those choices and no free text.
// - `plan <task> | <task> ...` drafts those tasks with `write_todos`; send it with `plan_mode`.
// - `stall <gate>` waits at that FileGate, then answers.
// - `describe` answers `saw <n> images`, counting the image bytes it got.
// - The approved plan's message answers `executed plan`.
// After its tool results arrive, the model answers `<tool>: <output>` per result, joined with `; `.
// At startup the Worker stores a PNG in its LocalDirectoryMediaStore and writes the reference, in
// snake_case JSON, to image-reference.json in the state directory.
// Usage: node remote_session_worker.mjs <TypeScript core directory> <Flow type>, with
// REMOTE_SESSION_STATE_DIR naming the state directory.

import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [coreDirectory, flowType] = process.argv.slice(2);
const stateDirectory = process.env.REMOTE_SESSION_STATE_DIR;
const load = (path) => import(pathToFileURL(join(coreDirectory, "dist", "src", path)).href);
const { LocalDirectoryMediaStore } = await load("index.js");
const { createBuiltinAgentSessionFlow, functionTool } = await load("builtin-loop/index.js");
const { FileGate, ScriptedModelClient, runAgentWorkerProcess } = await load("testing/index.js");

const approvedPlanPrefix = "[Dex] The user approved plan revision";
// A 1x1 PNG.
const pngBytes = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==",
  "base64",
);

const split = (text) =>
  text
    .split("|")
    .map((part) => part.trim())
    .filter((part) => part !== "");

const respond = async (request) => {
  const command = ScriptedModelClient.userTexts(request).at(-1) ?? "";
  const results = ScriptedModelClient.toolResults(request);
  const [verb = "", ...words] = command.split(" ");
  const rest = words.join(" ");
  if (results.length > 0) {
    if (verb === "plan") {
      return { text: `drafted ${split(rest).length} tasks` };
    }
    return { text: results.map((result) => `${result.toolName}: ${result.output}`).join("; ") };
  }
  if (command.startsWith(approvedPlanPrefix)) {
    return { text: "executed plan" };
  }
  switch (verb) {
    case "say":
      return { text: `said: ${rest}` };
    case "publish":
      return { toolCalls: [{ toolName: "publish_note", arguments: { message: rest } }] };
    case "ask": {
      const [question = "", ...choices] = split(rest);
      return {
        toolCalls: [{ toolName: "ask_user", arguments: { question, choices, allows_free_text: false } }],
      };
    }
    case "plan": {
      const todos = split(rest).map((content) => ({ content, status: "pending" }));
      return { toolCalls: [{ toolName: "write_todos", arguments: { todos } }] };
    }
    case "stall": {
      const gate = new FileGate(stateDirectory, rest);
      gate.markReached();
      await gate.waitForRelease(request.cancellationSignal);
      return { text: `stalled at ${rest}` };
    }
    case "describe":
      return { text: `saw ${request.imageBytes?.size ?? 0} images` };
    default:
      throw new Error(`unknown scripted command ${JSON.stringify(command)}`);
  }
};

const mediaStore = new LocalDirectoryMediaStore(join(stateDirectory, "media"));
const reference = await mediaStore.putImage(pngBytes);
writeFileSync(
  join(stateDirectory, "image-reference.json"),
  JSON.stringify({
    type: reference.type,
    uri: reference.uri,
    mime_type: reference.mimeType,
    sha256: reference.sha256,
    size_bytes: reference.sizeBytes,
  }),
);
const flow = createBuiltinAgentSessionFlow(
  {
    name: "remote-session-test",
    instructions: "You are a scripted test agent.",
    model: new ScriptedModelClient(respond),
    tools: [
      functionTool({
        name: "publish_note",
        description: "Publish a note.",
        parameters: {
          type: "object",
          properties: { message: { type: "string" } },
          required: ["message"],
          additionalProperties: false,
        },
        effect: "non_idempotent_write",
        execute: ({ message }) => `published ${message}`,
      }),
    ],
    builtInTools: ["ask_user", "write_todos"],
    planModeEnabled: true,
  },
  flowType,
  { mediaStore },
);
await runAgentWorkerProcess([flow]);
