// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { FlowErrorType, Registry, StepList, SubFlow, type Flow } from "../src/index.js";
import {
  ConditionResults,
  Context as ProtoContext,
  FlowErrorType as ProtoFlowErrorType,
  FlowStatus as ProtoFlowStatus,
} from "../src/gen/dex.js";
import { registeredFlowByName } from "../src/flow.js";
import { InvocationContext } from "../src/invocation-context.js";

class ParentFlow implements Flow<void> {
  public getFlowType(): string { return "ParentFlow"; }
  public getSteps(): StepList<void> { return StepList.empty(); }
}

test("SubFlow results map abandoned and unrecognized error types", () => {
  const flow = new ParentFlow();
  const registered = registeredFlowByName(new Registry([flow]), flow.getFlowType());
  const context = new InvocationContext("execute", registered, ProtoContext.create(), [], [],
    ConditionResults.create({
      subFlowResults: [
        {
          flowStatus: ProtoFlowStatus.FLOW_STATUS_FAILED,
          errorType: ProtoFlowErrorType.FLOW_ERROR_TYPE_SUB_FLOW_ABANDONED,
          errorMessage: "abandoned by a Time Travel",
        },
        {
          flowStatus: ProtoFlowStatus.FLOW_STATUS_FAILED,
          errorType: 99 as ProtoFlowErrorType,
        },
      ],
    }));

  const abandoned = SubFlow.getConditionResults(context, 0);
  assert.equal(abandoned.status, "failed");
  assert.equal(abandoned.errorType, FlowErrorType.SUB_FLOW_ABANDONED);
  assert.equal(abandoned.errorMessage, "abandoned by a Time Travel");

  const unrecognized = SubFlow.getConditionResults(context, 1);
  assert.equal(unrecognized.status, "failed");
  assert.equal(unrecognized.errorType, undefined);
});
