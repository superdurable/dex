// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Timing and file ownership cases that the command cannot reach reliably as a child process.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { WorkspaceFiles } from "@superdurable/dex-agents-cli";

import { ProjectContextFiles } from "../../src/index.js";
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

  test("an ancestor's context file linked into its own subtree is skipped, and a sibling link loads", async () => {
    const root = newRoot();
    const workspace = join(root, "workspace");
    mkdirSync(join(root, "sub"), { recursive: true });
    mkdirSync(workspace);
    writeFileSync(join(root, "sub", "rules.md"), "Subtree rules.\n");
    symlinkSync(join(root, "sub", "rules.md"), join(root, "AGENTS.md"));
    writeFileSync(join(workspace, "rules.md"), "Workspace rules.\n");
    symlinkSync(join(workspace, "rules.md"), join(workspace, "AGENTS.md"));
    const warnings: string[] = [];
    const loader = new ProjectContextFiles(workspace, join(root, "agent"), (line) => warnings.push(line));
    const files = await loader.load();
    assert.deepEqual(
      files.map((file) => file.content),
      ["Workspace rules.\n"],
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
    const warnings: string[] = [];
    // The loader acts for another user, so the current user's ancestor file counts as foreign.
    const loader = new ProjectContextFiles(workspace, join(root, "agent"), (line) => warnings.push(line), currentUserId + 12_345);
    const files = await loader.load();
    assert.deepEqual(
      files.map((file) => file.content),
      ["Workspace instructions.\n"],
    );
    assert.match(warnings.join("\n"), new RegExp(`${root}/AGENTS\\.md: it belongs to user ${currentUserId}, not to you or root`));
  });
});
