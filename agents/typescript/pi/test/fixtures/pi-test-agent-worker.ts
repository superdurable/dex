// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";

import {
  createPiTestAgentFlow,
  piTestMarkerDirectoryVariable,
  piTestModelServerVariable,
  piWorkerKillFlowType,
} from "./pi-test-agent.js";

const modelServerBaseUrl = process.env[piTestModelServerVariable];
const markerDirectory = process.env[piTestMarkerDirectoryVariable];
if (modelServerBaseUrl === undefined || markerDirectory === undefined) {
  throw new Error(`${piTestModelServerVariable} and ${piTestMarkerDirectoryVariable} must be set`);
}
await runAgentWorkerProcess([createPiTestAgentFlow({ flowType: piWorkerKillFlowType, modelServerBaseUrl, markerDirectory })]);
