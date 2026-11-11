// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Timing and file ownership cases that the command cannot reach reliably as a child process.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readdirSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import type { DeferredToolExecution } from "@superdurable/dex-agents";
import { TurnConfigResolver, WorkspaceFiles, resolveConfig, type HarnessContext } from "@superdurable/dex-agents-cli-core";

import { PiCodingToolSource, ProjectContextFiles, piHarness } from "../../src/index.js";
import { PiToolCallOperations } from "../../src/pi-tool-call-operations.js";

function newRoot(): string {
  return realpathSync.native(mkdtempSync(join(tmpdir(), "dex-agents-pi-local-")));
}

describe("local file access of the Pi tools", () => {
  test("find stops walking when its call is cancelled", async () => {
    const workspace = newRoot();
    for (let index = 0; index < 20; index += 1) {
      mkdirSync(join(workspace, `directory-${index}`, "nested"), { recursive: true });
    }
    const operations = new PiToolCallOperations(new WorkspaceFiles(workspace));
    const cancellation = new AbortController();
    cancellation.abort(new Error("the turn was cancelled"));
    operations.bindCancellation(cancellation.signal);
    await assert.rejects(operations.findMatchingPaths("*", workspace, 1000), /the turn was cancelled/);
  });

  test("a cancelled bash call with a cut output leaves no temporary file", async () => {
    const workspace = newRoot();
    const command = "seq 1 200000; sleep 30";
    const effective = resolveConfig(
      { local_tools: { browser: false, shell: { enabled: true, allow: [command] } } },
      piHarness.configSection,
      piHarness.defaultModel,
    );
    const turns = new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined));
    const context: HarnessContext = {
      flowType: "PiLocalFileAccessTest",
      config: effective,
      workspace,
      instructions: null,
      harnessOptions: {},
      toolSources: [],
      resolveTurn: (settings) => turns.resolveTurn(settings),
      currentTurn: () => turns.currentTurn(),
      resolveToolStepPermissions: (step) => turns.resolveToolStep(step),
    };
    const outputFiles = (): string[] => readdirSync(tmpdir()).filter((name) => name.startsWith("pi-bash-"));
    const filesBefore = new Set(outputFiles());
    const cancellation = new AbortController();
    const execution = {
      flowId: "pi-local-file-access",
      toolCall: {
        toolCallId: "call-1",
        turnId: "turn-1",
        toolName: "bash",
        arguments: JSON.stringify({ command }),
        effect: "non_idempotent_write",
        requiresApproval: false,
      },
      idempotencyKey: "pi-local-file-access/turn-1/call-1",
      attempt: 1,
      events: { writeEvent: () => undefined },
      cancellationSignal: cancellation.signal,
      deadline: undefined,
      heartbeat: () => Promise.resolve(),
    } as unknown as DeferredToolExecution;
    const running = new PiCodingToolSource(context).executeDeferredToolCall(execution);
    await delay(2_000);
    assert.ok(outputFiles().some((name) => !filesBefore.has(name)), "Pi keeps the cut output in a temporary file");
    cancellation.abort(new Error("the turn was cancelled"));
    await assert.rejects(running);
    assert.deepEqual(outputFiles().filter((name) => !filesBefore.has(name)), []);
  });

  test("an ancestor's own link to a sibling loads, and a link into its subtree is skipped", async () => {
    const root = newRoot();
    const repository = join(root, "repository");
    const workspace = join(repository, "packages", "app");
    mkdirSync(join(root, "sub"), { recursive: true });
    mkdirSync(workspace, { recursive: true });
    writeFileSync(join(root, "sub", "rules.md"), "Subtree rules.\n");
    symlinkSync(join(root, "sub", "rules.md"), join(root, "AGENTS.md"));
    writeFileSync(join(repository, "CLAUDE.md"), "Repository rules.\n");
    symlinkSync("CLAUDE.md", join(repository, "AGENTS.md"));
    writeFileSync(join(workspace, "rules.md"), "Workspace rules.\n");
    symlinkSync(join(workspace, "rules.md"), join(workspace, "AGENTS.md"));
    const warnings: string[] = [];
    const loader = new ProjectContextFiles(workspace, join(root, "agent"), (line) => warnings.push(line));
    const files = await loader.load();
    assert.deepEqual(
      files.map((file) => file.content),
      ["Repository rules.\n", "Workspace rules.\n"],
    );
    assert.match(warnings.join("\n"), new RegExp(`${root}/AGENTS\\.md: it resolves to .*outside its directory`));
  });

  test("an ancestor's context file of another user is skipped; the workspace's is not checked", async (context) => {
    const currentUserId = typeof process.getuid === "function" ? process.getuid() : undefined;
    if (currentUserId === undefined || currentUserId === 0) {
      context.skip("root's files always pass the ownership check, so the test needs a non-root user");
      return;
    }
    const root = newRoot();
    const workspace = join(root, "workspace");
    mkdirSync(workspace);
    writeFileSync(join(root, "AGENTS.md"), "Shared directory instructions.\n");
    writeFileSync(join(workspace, "AGENTS.md"), "Workspace instructions.\n");
    const parent = join(root, "parent");
    mkdirSync(join(parent, "workspace"), { recursive: true });
    writeFileSync(join(parent, "CLAUDE.md"), "Linked instructions.\n");
    symlinkSync("CLAUDE.md", join(parent, "AGENTS.md"));
    writeFileSync(join(parent, "workspace", "AGENTS.md"), "Nested workspace instructions.\n");
    const warnings: string[] = [];
    // The loader acts for another user, so the current user's ancestor file counts as foreign.
    const loader = new ProjectContextFiles(workspace, join(root, "agent"), (line) => warnings.push(line), currentUserId + 12_345);
    const files = await loader.load();
    assert.deepEqual(
      files.map((file) => file.content),
      ["Workspace instructions.\n"],
    );
    assert.match(warnings.join("\n"), new RegExp(`${root}/AGENTS\\.md: it belongs to user ${currentUserId}, not to you or root`));

    // A link that another user owns is skipped before its target is read.
    const linkWarnings: string[] = [];
    const linkLoader = new ProjectContextFiles(
      join(parent, "workspace"),
      join(root, "agent"),
      (line) => linkWarnings.push(line),
      currentUserId + 12_345,
    );
    assert.deepEqual(
      (await linkLoader.load()).map((file) => file.content),
      ["Nested workspace instructions.\n"],
    );
    assert.match(
      linkWarnings.join("\n"),
      new RegExp(`${parent}/AGENTS\\.md: it is a link that belongs to user ${currentUserId}, not to you or root`),
    );
  });
});
