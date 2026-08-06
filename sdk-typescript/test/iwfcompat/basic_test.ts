// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  ActiveStepSearchMode,
  IdReusePolicy,
  StepExecutionId,
  doubleCodec,
  type Client,
  type FlowConfig,
  type FlowInfo,
  type StartFlowOptions,
} from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileBasicAndReuse(client: Client): Promise<void> {
  const options: StartFlowOptions = {
    timeoutMs: 10_000,
    idReusePolicy: IdReusePolicy.ALLOW_IF_NOT_RUNNING,
  };
  await client.startFlow(flows.BASIC, "basic", 10, options);
  const output: number = await client.waitForFlow("basic", doubleCodec);
  await client.startFlow(flows.ABNORMAL_EXIT, "abnormal", 10, options);
  await client.startFlow(flows.BASIC, "abnormal", output, options);
}

export async function compileEmptyAndModelInputs(client: Client): Promise<void> {
  await client.startFlow(flows.EMPTY_INPUT, "empty", undefined);
  await client.startFlow(flows.MODEL_INPUT, "model", { value: 10 });
}

export async function compileFailurePolicyAndConfigOverride(client: Client): Promise<void> {
  const config: FlowConfig = {
    activeStepSearchMode: ActiveStepSearchMode.ALL,
    workerTarget: { address: "worker:8803" },
  };
  const options: StartFlowOptions = { configOverride: config };
  await client.startFlow(flows.PROCEED_ON_WAIT_FAILURE, "recover", "input", options);
  await client.startFlow(flows.MIXED_WAIT, "mixed", 0, options);
  await client.updateFlowConfig("mixed", config);
}

export async function compileDescribeAndStepWait(client: Client): Promise<void> {
  const info: FlowInfo = await client.describeFlow("basic");
  await client.waitForStepCompletion(
    "basic",
    StepExecutionId.of("BasicSecondStep"),
    5_000,
  );
  void info;
}
