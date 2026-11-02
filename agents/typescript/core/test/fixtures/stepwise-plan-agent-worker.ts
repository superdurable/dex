// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "../../src/testing/index.js";
import {
  createPlanAgentWorkerFlows,
  planAgentMarkerDirectoryVariable,
  planAgentUserMessageGateVariable,
} from "./stepwise-plan-agent.js";

const markerDirectory = process.env[planAgentMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${planAgentMarkerDirectoryVariable} is not set`);
}
await runAgentWorkerProcess(
  createPlanAgentWorkerFlows(markerDirectory, process.env[planAgentUserMessageGateVariable]),
);
