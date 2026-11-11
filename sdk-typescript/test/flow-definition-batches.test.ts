// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { FlowDefinitionUploadOutcome } from "../src/index.js";
import {
  flowDefinitionBatches,
  maxFlowDefinitionUploadContentBytes,
  uploadResultsFromResponse,
  type PreparedFlowDefinition,
} from "../src/flow-definition-upload.js";
import { FlowDefinitionUploadOutcome as ProtoFlowDefinitionUploadOutcome } from "../src/gen/dex.js";

const mib = 1024 * 1024;

function definitionsWithSizes(...sizes: number[]): PreparedFlowDefinition[] {
  return sizes.map((size, index) => ({
    flowType: `Flow${index}`,
    content: new Uint8Array(size),
    generatedAt: new Date("2026-11-01T00:00:00Z"),
    origin: `test ${index}`,
  }));
}

function batchSizes(definitions: readonly PreparedFlowDefinition[]): number[] {
  return flowDefinitionBatches(definitions).map((batch) => batch.length);
}

test("Flow definition batches split at 256 items", () => {
  assert.deepEqual(flowDefinitionBatches([]), []);
  assert.deepEqual(batchSizes(definitionsWithSizes(...Array<number>(600).fill(1))), [256, 256, 88]);
});

test("Flow definition batches split before exceeding 16 MiB of content", () => {
  assert.deepEqual(batchSizes(definitionsWithSizes(6 * mib, 6 * mib, 6 * mib)), [2, 1]);
  assert.deepEqual(batchSizes(definitionsWithSizes(maxFlowDefinitionUploadContentBytes - 1, 1, 1)), [2, 1]);
});

test("Flow definition above 16 MiB goes alone and order is kept", () => {
  const definitions = definitionsWithSizes(1, 20 * mib, 1);
  assert.deepEqual(batchSizes(definitions), [1, 1, 1]);
  assert.deepEqual(flowDefinitionBatches(definitions).flat(), definitions);
});

test("unknown Flow definition upload outcome is unspecified", () => {
  const results = uploadResultsFromResponse({
    results: [
      {
        flowType: "A",
        outcome: ProtoFlowDefinitionUploadOutcome.FLOW_DEFINITION_UPLOAD_OUTCOME_UNSPECIFIED,
        storedDigest: "",
        storedGeneratedAt: undefined,
      },
      {
        flowType: "B",
        outcome: 99 as ProtoFlowDefinitionUploadOutcome,
        storedDigest: "",
        storedGeneratedAt: undefined,
      },
    ],
  });
  assert.deepEqual(results.map((result) => result.outcome), [
    FlowDefinitionUploadOutcome.UNSPECIFIED,
    FlowDefinitionUploadOutcome.UNSPECIFIED,
  ]);
});
