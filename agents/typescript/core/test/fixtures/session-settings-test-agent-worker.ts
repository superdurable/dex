// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "../../src/testing/index.js";
import {
  createSessionSettingsTestAgentFlow,
  sessionSettingsMarkerDirectoryVariable,
} from "./session-settings-test-agent.js";

const markerDirectory = process.env[sessionSettingsMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${sessionSettingsMarkerDirectoryVariable} is not set`);
}
await runAgentWorkerProcess([createSessionSettingsTestAgentFlow(markerDirectory)]);
