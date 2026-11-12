// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Project context file cases that the command cannot reach reliably as a child process.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { ProjectContextFiles } from "../../src/index.js";

function newRoot(): string {
  return realpathSync.native(mkdtempSync(join(tmpdir(), "dex-agents-pi-local-")));
}

describe("project context files of the Pi command", () => {
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
