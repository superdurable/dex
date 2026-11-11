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
  createScriptedMastraAgentFlow,
  scriptedMastraMarkerDirectoryVariable,
  scriptedMastraMediaDirectoryVariable,
} from "./scripted-mastra-agent.js";

const markerDirectory = process.env[scriptedMastraMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${scriptedMastraMarkerDirectoryVariable} is not set`);
}
const mediaDirectory = process.env[scriptedMastraMediaDirectoryVariable];
await runAgentWorkerProcess([
  createScriptedMastraAgentFlow(
    markerDirectory,
    mediaDirectory === undefined || mediaDirectory === ""
      ? {}
      : { mediaStore: new LocalDirectoryMediaStore(mediaDirectory) },
  ),
]);
