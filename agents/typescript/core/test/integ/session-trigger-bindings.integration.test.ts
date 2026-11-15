// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { renderSessionFlowGraph, resolveSessionTriggerBinding, type SessionTriggerBindingSpec } from "../../src/index.js";
import {
  parseSessionTriggerBindingFlag,
  readSessionTriggerBindingsFile,
} from "../../src/agent-session-trigger-bindings.js";
import { conformanceSuiteDirectory, readConformanceJson } from "../fixtures/conformance-suite.js";

interface TriggerBindingsSuite {
  readonly package_directory: string;
  readonly flow_type: string;
  readonly action_permission: string;
  readonly cases: readonly { readonly flag?: string; readonly entry: SessionTriggerBindingSpec; readonly binding: unknown }[];
  readonly file_cases: readonly {
    readonly name: string;
    readonly package_directory: string;
    readonly flag?: string;
    readonly entry: Record<string, unknown>;
    readonly binding?: unknown;
    readonly error?: string;
  }[];
  readonly rendered_sha256: string;
}

const suite = readConformanceJson<TriggerBindingsSuite>("session_graph_trigger_bindings.json");
const packageDirectory = path.join(conformanceSuiteDirectory, suite.package_directory);
const command = fileURLToPath(new URL("../../src/agent-session-flow-graph-cli.js", import.meta.url));
const graphOptions = { flowType: suite.flow_type, actionPermission: suite.action_permission };

function resolveCase(index: number) {
  return resolveSessionTriggerBinding({ ...suite.cases[index]!.entry, packageDirectory });
}

function projectWithInstalledPackage(): string {
  const project = mkdtempSync(path.join(tmpdir(), "session-trigger-bindings-"));
  const installed = path.join(project, "node_modules", "@dex-test", "messages");
  mkdirSync(path.dirname(installed), { recursive: true });
  cpSync(packageDirectory, installed, { recursive: true });
  return project;
}

function runCommand(cwd: string, ...options: string[]) {
  const base = ["--flow-type", suite.flow_type, "--permission", suite.action_permission];
  return spawnSync(process.execPath, [command, ...base, ...options], { cwd, encoding: "utf8" });
}

describe("session graph Connector Trigger bindings", () => {
  test("each resolved binding is the entry that the Flow analyzer derives from code", () => {
    suite.cases.forEach((suiteCase, index) => {
      assert.equal(JSON.stringify(resolveCase(index)), JSON.stringify(suiteCase.binding), `case ${index}`);
    });
  });

  test("the flag form parses to the suite's entry", () => {
    for (const suiteCase of suite.cases) {
      if (suiteCase.flag !== undefined) {
        assert.deepEqual(parseSessionTriggerBindingFlag(suiteCase.flag), suiteCase.entry);
      }
    }
    for (const invalid of ["=p@1.0.0:t:b", "c=p:t:b", "c=@scope/p:t:b", "c=p@1.0.0:t:", "c=p@1.0.0:t", "c"]) {
      assert.throws(() => parseSessionTriggerBindingFlag(invalid), /--trigger-binding/, invalid);
    }
  });

  test("a graph with two bindings lists them in order and renders the same bytes as every core", () => {
    const text = renderSessionFlowGraph({ ...graphOptions, connectorTriggerBindings: [resolveCase(0), resolveCase(1)] });
    assert.equal(createHash("sha256").update(text).digest("hex"), suite.rendered_sha256);
    const graph = JSON.parse(text) as { v2: Record<string, unknown> };
    assert.equal(Object.keys(graph.v2).at(-1), "connectorTriggerBindings");
    assert.deepEqual(
      graph.v2["connectorTriggerBindings"],
      suite.cases.map((suiteCase) => suiteCase.binding),
    );
  });

  test("a graph with one binding adds only that binding", () => {
    const withBinding = JSON.parse(renderSessionFlowGraph({ ...graphOptions, connectorTriggerBindings: [resolveCase(0)] }));
    const without = JSON.parse(renderSessionFlowGraph(graphOptions));
    assert.deepEqual(withBinding.v2.connectorTriggerBindings, [suite.cases[0]!.binding]);
    delete withBinding.v2.connectorTriggerBindings;
    assert.deepEqual(withBinding, without);
  });

  test("without bindings the graph text is unchanged", () => {
    const without = renderSessionFlowGraph(graphOptions);
    assert.equal(renderSessionFlowGraph({ ...graphOptions, connectorTriggerBindings: [] }), without);
    assert.equal(runCommand(process.cwd()).stdout, without);
  });

  test("an unknown Trigger, a missing package, and a mismatched version fail with the reason", () => {
    const entry = suite.cases[0]!.entry;
    assert.throws(
      () => resolveSessionTriggerBinding({ ...entry, packageDirectory, triggerName: "messageDeleted" }),
      /has no Trigger messageDeleted; its Triggers: replyCreated, threadCreated/,
    );
    const empty = mkdtempSync(path.join(tmpdir(), "session-trigger-bindings-empty-"));
    assert.throws(() => resolveSessionTriggerBinding(entry, empty), /package @dex-test\/messages is not installed in node_modules above/);
    assert.throws(
      () => resolveSessionTriggerBinding({ ...entry, packageDirectory: empty }),
      /has no Connector manifest at/,
    );
    assert.throws(
      () => resolveSessionTriggerBinding({ ...entry, packageDirectory, packageVersion: "2.3.5" }),
      /is for @dex-test\/messages@2\.3\.4, not @dex-test\/messages@2\.3\.5/,
    );
    assert.throws(
      () =>
        resolveSessionTriggerBinding({
          ...entry,
          packageDirectory,
          configurationUI: { units: [{ id: "a", unitId: "textInput", label: "A", bindings: [{ port: "p", jsonPointer: "x" }] }] },
        }),
      /invalid JSON pointer/,
    );
  });

  test("every file case resolves to its entry or fails with its error, like every core", () => {
    const directory = mkdtempSync(path.join(tmpdir(), "session-trigger-binding-cases-"));
    for (const fileCase of suite.file_cases) {
      const entry =
        "packageDirectory" in fileCase.entry
          ? fileCase.entry
          : { ...fileCase.entry, packageDirectory: path.join(conformanceSuiteDirectory, fileCase.package_directory) };
      const bindingsFile = path.join(directory, "trigger-bindings.json");
      writeFileSync(bindingsFile, JSON.stringify([entry]));
      const resolve = () => readSessionTriggerBindingsFile(bindingsFile).map((spec) => resolveSessionTriggerBinding(spec));
      if (fileCase.error === undefined) {
        assert.equal(JSON.stringify(resolve()), JSON.stringify([fileCase.binding]), fileCase.name);
      } else {
        assert.throws(resolve, (error: Error) => error.message.includes(fileCase.error!), fileCase.name);
      }
      if (fileCase.flag !== undefined) {
        assert.deepEqual(parseSessionTriggerBindingFlag(fileCase.flag), fileCase.entry, fileCase.name);
      }
    }
  });

  test("two bindings with one identity are refused", () => {
    assert.throws(
      () => renderSessionFlowGraph({ ...graphOptions, connectorTriggerBindings: [resolveCase(0), resolveCase(0)] }),
      /declared twice/,
    );
  });

  test("the command reads installed packages and a bindings file", () => {
    const project = projectWithInstalledPackage();
    const expected = renderSessionFlowGraph({ ...graphOptions, connectorTriggerBindings: [resolveCase(0), resolveCase(1)] });
    const bindingsFile = path.join(project, "trigger-bindings.json");
    writeFileSync(
      bindingsFile,
      JSON.stringify([{ ...suite.cases[1]!.entry, packageDirectory: "node_modules/@dex-test/messages" }]),
    );
    const run = runCommand(project, "--trigger-binding", suite.cases[0]!.flag!, "--trigger-bindings", bindingsFile);
    assert.equal(run.status, 0, run.stderr);
    assert.equal(run.stdout, expected);
    const single = runCommand(project, "--trigger-binding", suite.cases[0]!.flag!);
    assert.equal(single.stdout, renderSessionFlowGraph({ ...graphOptions, connectorTriggerBindings: [resolveCase(0)] }));

    const unknown = runCommand(project, "--trigger-binding", "workspace=@dex-test/messages@2.3.4:messageDeleted:replies");
    assert.equal(unknown.status, 2);
    assert.match(unknown.stderr, /has no Trigger messageDeleted/);
    const missing = runCommand(project, "--trigger-binding", "workspace=@dex-test/missing@1.0.0:replyCreated:replies");
    assert.equal(missing.status, 2);
    assert.match(missing.stderr, /package @dex-test\/missing is not installed/);
    const malformed = runCommand(project, "--trigger-binding", "workspace");
    assert.equal(malformed.status, 2);
    assert.match(malformed.stderr, /must be <connectionName>=<packageName>@<packageVersion>:<triggerName>\[#<factoryName>\]:<bindingName>/);
  });
});
