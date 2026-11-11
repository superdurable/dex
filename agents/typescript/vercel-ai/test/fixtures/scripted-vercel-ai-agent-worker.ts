// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { LocalDirectoryMediaStore } from "@superdurable/dex-agents";
import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";

import {
  createScriptedVercelAiAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
  scriptedAgentMediaDirectoryVariable,
} from "./scripted-vercel-ai-agent.js";

const markerDirectory = process.env[scriptedAgentMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${scriptedAgentMarkerDirectoryVariable} is not set`);
}
const mediaDirectory = process.env[scriptedAgentMediaDirectoryVariable];
await runAgentWorkerProcess([
  createScriptedVercelAiAgentFlow(
    markerDirectory,
    mediaDirectory === undefined || mediaDirectory === ""
      ? {}
      : { mediaStore: new LocalDirectoryMediaStore(mediaDirectory) },
  ),
]);
