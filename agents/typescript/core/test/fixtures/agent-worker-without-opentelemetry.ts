// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A Worker child that cannot resolve @opentelemetry/*, as when the optional peer is not installed.

import { appendFileSync } from "node:fs";
import { registerHooks } from "node:module";

import { blockedImportLogVariable } from "./blocked-imports.js";

const blockedImportLogPath = process.env[blockedImportLogVariable];
if (blockedImportLogPath === undefined || blockedImportLogPath === "") {
  throw new Error(`${blockedImportLogVariable} is not set`);
}
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith("@opentelemetry/")) {
      appendFileSync(blockedImportLogPath, `${specifier}\n`);
      throw Object.assign(new Error(`Cannot find module '${specifier}'`), { code: "MODULE_NOT_FOUND" });
    }
    return nextResolve(specifier, context);
  },
});

// Imported after the hook, so the core cannot load OpenTelemetry before it is blocked.
const { runAgentWorkerProcess } = await import("../../src/testing/index.js");
const { createScriptedAgentFlow, scriptedAgentMarkerDirectoryVariable } = await import(
  "./scripted-agent-runner.js"
);
const markerDirectory = process.env[scriptedAgentMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${scriptedAgentMarkerDirectoryVariable} is not set`);
}
await runAgentWorkerProcess([createScriptedAgentFlow(markerDirectory)]);
