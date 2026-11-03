// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import test from "node:test";

import {
  ExecuteFailure,
  StepList,
  gracefulComplete,
  stringCodec,
  type AsyncContext,
  type Codec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";
import { flowId, withEnvironment } from "./environment.js";

type MarkScenario = "complete" | "failAfterMark" | "failBeforeMark";

class RecordRecoveryProgress implements Step<MarkScenario> {
  public readonly inputCodec = stringCodec as Codec<MarkScenario>;

  public getStepType(): string {
    return "TypeScriptRecordRecoveryProgress";
  }

  public execute(context: Context): StepDecision {
    if (context.recoveryError === undefined) throw new Error("recovery error is missing");
    return gracefulComplete(context.recoveryError.attemptProgress);
  }
}

class MarkedEffect implements Step<MarkScenario> {
  public readonly inputCodec = stringCodec as Codec<MarkScenario>;
  public effects = 0;

  public constructor(private readonly recovery: RecordRecoveryProgress) {}

  public getStepType(): string {
    return "TypeScriptMarkedEffect";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { maximumAttempts: 1 },
      executeFailure: ExecuteFailure.proceedTo(this.recovery),
    };
  }

  public async execute(context: AsyncContext, scenario: MarkScenario): Promise<StepDecision> {
    if (scenario === "failBeforeMark") throw new Error("the effect never started");
    await context.markAttemptStarted();
    this.effects += 1;
    if (scenario === "failAfterMark") throw new Error("the effect may have run");
    return gracefulComplete("completed");
  }
}

class MarkedEffectFlow implements Flow<MarkScenario> {
  public readonly recovery = new RecordRecoveryProgress();
  public readonly effect = new MarkedEffect(this.recovery);

  public getFlowType(): string {
    return "TypeScriptMarkedEffectFlow";
  }

  public getSteps(): StepList<MarkScenario> {
    return StepList.startStep(this.effect).otherSteps(this.recovery);
  }
}

test("markAttemptStarted commits before the effect and reports progress to the failure route", async () => {
  for (const [scenario, expectedOutput, expectedEffects] of [
    ["complete", "completed", 1],
    ["failAfterMark", "started", 1],
    ["failBeforeMark", "assigned", 0],
  ] as const) {
    const flow = new MarkedEffectFlow();
    await withEnvironment([flow], async ({ client }) => {
      const id = flowId(`typescript-attempt-started-${scenario}`);
      await client.startFlow(flow, id, scenario);
      const result = await client.waitForFlow(id, 30_000);
      assert.equal(result.status, "completed", result.errorMessage ?? "Flow did not complete");
      assert.equal(result.singleOutput(stringCodec), expectedOutput);
      assert.equal(flow.effect.effects, expectedEffects);
    });
  }
});
