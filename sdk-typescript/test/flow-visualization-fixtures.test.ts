// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

import ts from "typescript";

import { Registry, type Flow } from "../src/index.js";
import { Context as ProtoContext, InvokeExecuteMethodRequest } from "../src/gen/dex.js";
import { registeredFlow, registeredStepTarget } from "../src/flow.js";
import { codecOrJson, encodeValue, type ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";

// The dexcli visualizer golden for this fixture must match what the SDK routes at runtime.
const fixtureDirectory = fileURLToPath(new URL("../../../cli/internal/flowviz/testdata/typescript/", import.meta.url));
const fixtureInputs = ["instance", "reference", "movement", "complete"];

interface GoldenGraph {
  readonly nodes: readonly { readonly id: string; readonly kind: string; readonly name: string; readonly parentId?: string }[];
  readonly edges: readonly { readonly kind: string; readonly from: string; readonly to: string }[];
}

test("step instances visualization matches runtime routing", async () => {
  const fixture = await loadFixture("step_instances");
  const FlowClass = fixture["StepInstancesFlow"] as new () => Flow<string>;
  const flow = new FlowClass();
  const registry = new Registry([flow]);
  const registered = registeredFlow(registry, flow);
  const hydrator = {
    hydrateAll: async (_flowId: string, values: readonly unknown[]) => values,
  } as unknown as ValueHydrator;
  const dispatcher = new WorkerDispatcher(registry, hydrator, () => 3);
  const routes = new Map<string, Set<string>>();
  for (const step of registered.steps) {
    const stepRoutes = new Set<string>();
    routes.set(step.name, stepRoutes);
    const failureTarget = step.step.getStepOptions?.()?.executeFailure?.step;
    if (failureTarget !== undefined) {
      stepRoutes.add(`failure_transition:${registeredStepTarget(registered, failureTarget).name}`);
    }
    for (const input of fixtureInputs) {
      const response = await dispatcher.invokeExecute(InvokeExecuteMethodRequest.create({
        context: ProtoContext.create(),
        flowType: flow.getFlowType(),
        stepType: step.name,
        stepInput: encodeValue(codecOrJson(step.step.inputCodec), input),
      }));
      const decision = response.stepDecision;
      for (const movement of decision?.nextSteps ?? []) stepRoutes.add(`transition:${movement.stepType}`);
      for (const stepType of [...(decision?.cancelStepTypes ?? []), ...(decision?.cancelSiblingStepTypes ?? [])]) {
        stepRoutes.add(`cancel:${stepType}`);
      }
    }
  }
  assert.deepEqual(routes, goldenRoutes("step_instances"));
});

async function loadFixture(name: string): Promise<Record<string, unknown>> {
  const source = readFileSync(join(fixtureDirectory, `${name}.ts`), "utf8");
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const sdkURL = new URL("../src/index.js", import.meta.url).href;
  const directory = mkdtempSync(join(tmpdir(), "dex-flow-visualization-"));
  try {
    const modulePath = join(directory, `${name}.mjs`);
    writeFileSync(modulePath, output.replaceAll("\"@superdurable/dex\"", JSON.stringify(sdkURL)));
    return await import(pathToFileURL(modulePath).href) as Record<string, unknown>;
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function goldenRoutes(name: string): Map<string, Set<string>> {
  const graph = JSON.parse(readFileSync(join(fixtureDirectory, `${name}.golden.json`), "utf8")) as GoldenGraph;
  const owners = new Map(graph.nodes.map((node) => [node.id, node.parentId ?? node.id]));
  const stepTypes = new Map(graph.nodes.filter((node) => node.kind === "step").map((node) => [node.id, node.name]));
  const routes = new Map([...stepTypes.values()].map((stepType) => [stepType, new Set<string>()]));
  for (const edge of graph.edges) {
    if (!["transition", "cancel", "failure_transition"].includes(edge.kind)) continue;
    const owner = stepTypes.get(owners.get(edge.from) ?? "");
    const target = stepTypes.get(edge.to);
    assert.ok(owner !== undefined && target !== undefined, `golden edge ${edge.from} -> ${edge.to}`);
    routes.get(owner)?.add(`${edge.kind}:${target}`);
  }
  return routes;
}
