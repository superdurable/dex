// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import * as builtinLoop from "../../src/builtin-loop/index.js";
import { BuiltinLoopAgentRunner, createBuiltinAgentSessionFlow } from "../../src/builtin-loop/index.js";
import * as stepwise from "../../src/stepwise/index.js";
import { StepwiseAgentRunner, createStepwiseAgentSessionFlow } from "../../src/stepwise/index.js";
import { ScriptedModelClient } from "../../src/testing/index.js";

const builtinLoopSubpath = "@superdurable/dex-agents/builtin-loop";
const stepwiseSubpath = "@superdurable/dex-agents/stepwise";

describe("the deprecated stepwise entry point", () => {
  test("exports every builtin-loop value and the two old names as the same values", () => {
    for (const [name, value] of Object.entries(builtinLoop)) {
      assert.equal((stepwise as Record<string, unknown>)[name], value, name);
    }
    assert.equal(StepwiseAgentRunner, BuiltinLoopAgentRunner);
    assert.equal(createStepwiseAgentSessionFlow, createBuiltinAgentSessionFlow);
  });

  test("builds a working session Flow under the old names", () => {
    const model = new ScriptedModelClient(() => ({ text: "Done." }));
    const flow = createStepwiseAgentSessionFlow({ name: "agent", instructions: "Help.", model }, "StepwiseAliasAgent");
    const runner: StepwiseAgentRunner = flow.runner;

    assert.ok(runner instanceof BuiltinLoopAgentRunner);
    assert.ok(new StepwiseAgentRunner({ name: "agent", instructions: "Help.", model }) instanceof BuiltinLoopAgentRunner);
  });

  test("resolves both package subpaths to the same values", async () => {
    const builtinLoopModule = (await import(builtinLoopSubpath)) as Record<string, unknown>;
    const stepwiseModule = (await import(stepwiseSubpath)) as Record<string, unknown>;

    assert.equal(typeof builtinLoopModule["createBuiltinAgentSessionFlow"], "function");
    assert.equal(stepwiseModule["createStepwiseAgentSessionFlow"], builtinLoopModule["createBuiltinAgentSessionFlow"]);
    assert.equal(stepwiseModule["StepwiseAgentRunner"], builtinLoopModule["BuiltinLoopAgentRunner"]);
  });
});
