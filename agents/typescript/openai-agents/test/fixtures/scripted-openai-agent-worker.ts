// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";

import {
  createScriptedOpenAIAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
} from "./scripted-openai-agent.js";

const markerDirectory = process.env[scriptedAgentMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${scriptedAgentMarkerDirectoryVariable} is not set`);
}
await runAgentWorkerProcess([createScriptedOpenAIAgentFlow(markerDirectory)]);
