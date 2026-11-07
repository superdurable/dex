// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { appendFileSync } from "node:fs";
import { join } from "node:path";

import { FileGate, runAgentWorkerProcess } from "../../src/testing/index.js";
import { createScriptedAgentFlow, scriptedAgentMarkerDirectoryVariable } from "./scripted-agent-runner.js";

export const spendLimitKillFlowType = "SpendLimitKillAgent";
export const spendLimitReportLogName = "spend-reports.jsonl";
export const spendLimitReportGateName = "after-report";

const markerDirectory = process.env[scriptedAgentMarkerDirectoryVariable];
if (markerDirectory !== undefined && markerDirectory !== "") {
  await runAgentWorkerProcess([
    createScriptedAgentFlow(markerDirectory, {
      flowType: spendLimitKillFlowType,
      spendLimit: {
        checkModelCall: () => Promise.resolve({ isAllowed: true }),
        reportModelUsage: async (report) => {
          appendFileSync(join(markerDirectory, spendLimitReportLogName), `${JSON.stringify(report)}\n`);
          const gate = new FileGate(markerDirectory, spendLimitReportGateName);
          gate.markReached();
          await gate.waitForRelease();
        },
      },
    }),
  ]);
}
