// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "../../src/testing/index.js";
import {
  createSteeringTestAgentFlow,
  gateFinishTurnCancellation,
  steeringBuilderGateVariable,
  steeringFinishCancellationGateVariable,
  steeringMarkerDirectoryVariable,
} from "./steering-test-agent.js";

const markerDirectory = process.env[steeringMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${steeringMarkerDirectoryVariable} is not set`);
}
const builderGate = process.env[steeringBuilderGateVariable];
const finishCancellationGate = process.env[steeringFinishCancellationGateVariable];
if (finishCancellationGate !== undefined) {
  gateFinishTurnCancellation(markerDirectory, finishCancellationGate);
}
await runAgentWorkerProcess([
  createSteeringTestAgentFlow(markerDirectory, builderGate === undefined ? {} : { builderGate }),
]);
