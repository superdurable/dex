// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, linkSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, renameSync, symlinkSync, writeFileSync } from "node:fs";
import { rename, symlink, unlink } from "node:fs/promises";
import { setImmediate as setImmediatePromise } from "node:timers/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import type { AgentTurnContext, ToolPolicy } from "@superdurable/dex-agents";

import { namespaceSessionFlowType, resolveConfig, resolveTurnToolPolicy } from "../../src/index.js";
import { WorkspaceFiles } from "../../src/local-tools/workspace-files.js";

function newWorkspace(): { root: string; workspace: string; files: WorkspaceFiles } {
  const root = realpathSync.native(mkdtempSync(join(tmpdir(), "dex-agents-boundary-")));
  const workspace = join(root, "workspace");
  mkdirSync(workspace);
  writeFileSync(join(root, "outside.txt"), "outside secret");
  mkdirSync(join(root, "outside-dir"));
  return { root, workspace, files: new WorkspaceFiles(workspace) };
}

describe("the workspace boundary of the file tools", () => {
  test("a hard link to an outside file is refused for reads, writes, and edits", async () => {
    const { root, workspace, files } = newWorkspace();
    linkSync(join(root, "outside.txt"), join(workspace, "linked.txt"));
    await assert.rejects(files.readFile("linked.txt"), /has 2 hard links/);
    await assert.rejects(files.writeFile("linked.txt", "changed"), /has 2 hard links/);
    await assert.rejects(files.editFile("linked.txt", "secret", "public"), /has 2 hard links/);
    assert.equal(readFileSync(join(root, "outside.txt"), "utf8"), "outside secret");
    assert.equal(await files.searchFiles("secret", "."), "no matches");
  });

  test("a directory swapped for a link to the outside is refused on the next call", async () => {
    const { root, workspace, files } = newWorkspace();
    mkdirSync(join(workspace, "notes"));
    assert.equal(await files.writeFile("notes/a.txt", "first"), "wrote 5 bytes to notes/a.txt");
    renameSync(join(workspace, "notes"), join(workspace, "notes-old"));
    symlinkSync(join(root, "outside-dir"), join(workspace, "notes"));
    await assert.rejects(files.writeFile("notes/a.txt", "second"), /outside the workspace/);
    await assert.rejects(files.readFile("notes/a.txt"), /outside the workspace/);
    await assert.rejects(files.listDirectory("notes"), /outside the workspace/);
    assert.equal(existsSync(join(root, "outside-dir", "a.txt")), false);
  });

  test("a file swapped for a link is refused, and writing never follows a link", async () => {
    const { root, workspace, files } = newWorkspace();
    writeFileSync(join(workspace, "data.txt"), "inside");
    assert.equal(await files.readFile("data.txt"), "inside");
    renameSync(join(workspace, "data.txt"), join(workspace, "data-old.txt"));
    symlinkSync(join(root, "outside.txt"), join(workspace, "data.txt"));
    await assert.rejects(files.readFile("data.txt"), /outside the workspace/);
    await assert.rejects(files.writeFile("data.txt", "overwrite"), /symbolic link/);
    assert.equal(readFileSync(join(root, "outside.txt"), "utf8"), "outside secret");
  });

  test("case variants resolve by the file system on a case-insensitive file system", async (context) => {
    const { root, workspace, files } = newWorkspace();
    if (!existsSync(workspace.toUpperCase())) {
      context.skip("the file system is case-sensitive");
      return;
    }
    writeFileSync(join(workspace, "Notes.txt"), "inside");
    assert.equal(await files.readFile(join(workspace.toUpperCase(), "NOTES.TXT")), "inside");
    await assert.rejects(files.readFile("../OUTSIDE.TXT"), /outside the workspace/);
    await assert.rejects(files.readFile(join(root.toUpperCase(), "OUTSIDE.TXT")), /outside the workspace/);
  });

  test("a search never returns outside content while a directory is swapped concurrently", async () => {
    const { root, workspace, files } = newWorkspace();
    writeFileSync(join(root, "outside-dir", "secret.txt"), "outside secret");
    mkdirSync(join(workspace, "notes"));
    writeFileSync(join(workspace, "notes", "secret.txt"), "inside note");
    let isSwapping = true;
    const swapper = (async () => {
      while (isSwapping) {
        await rename(join(workspace, "notes"), join(workspace, "notes-real"));
        await symlink(join(root, "outside-dir"), join(workspace, "notes"));
        await setImmediatePromise();
        await unlink(join(workspace, "notes"));
        await rename(join(workspace, "notes-real"), join(workspace, "notes"));
        await setImmediatePromise();
      }
    })();
    try {
      for (let iteration = 0; iteration < 300; iteration += 1) {
        const result = await files.searchFiles("secret", ".").catch((error: unknown) => String(error));
        assert.doesNotMatch(result, /outside secret/, `iteration ${iteration}`);
        const read = await files.readFile("notes/secret.txt").catch((error: unknown) => String(error));
        assert.doesNotMatch(read, /outside secret/, `iteration ${iteration}`);
      }
    } finally {
      isSwapping = false;
      await swapper;
    }
  });

  test("edit_file refuses to replace a file that changed after it was read", async () => {
    const { workspace, files } = newWorkspace();
    writeFileSync(join(workspace, "draft.txt"), "first version\n");
    const replaceFile = (files as unknown as { replaceFile: (...parameters: unknown[]) => Promise<string> }).replaceFile.bind(files);
    (files as unknown as { replaceFile: (...parameters: unknown[]) => Promise<string> }).replaceFile = async (
      ...parameters: unknown[]
    ) => {
      writeFileSync(join(workspace, "draft.txt"), "first version, edited by the user meanwhile\n");
      return replaceFile(...parameters);
    };
    await assert.rejects(files.editFile("draft.txt", "first", "second"), /changed after edit_file read it; read the file again/);
    assert.equal(readFileSync(join(workspace, "draft.txt"), "utf8"), "first version, edited by the user meanwhile\n");
  });

  test("edit_file replaces exactly one occurrence", async () => {
    const { workspace, files } = newWorkspace();
    writeFileSync(join(workspace, "todo.txt"), "buy milk\nbuy bread\n");
    await assert.rejects(files.editFile("todo.txt", "buy", "sell"), /occurs 2 times/);
    await assert.rejects(files.editFile("todo.txt", "eggs", "ham"), /does not occur/);
    await assert.rejects(files.editFile("missing.txt", "a", "b"), /does not exist/);
    assert.equal(await files.editFile("todo.txt", "milk", "oat milk $&"), "edited todo.txt: replaced 1 occurrence");
    assert.equal(readFileSync(join(workspace, "todo.txt"), "utf8"), "buy oat milk $&\nbuy bread\n");
  });
});

describe("session Flow types", () => {
  const base = { hostName: "host", userId: "501", configIdentity: "/home/a/.dex/agents/x.yaml", workspace: "/home/a/w" };

  test("the same environment gives the same type, and a changed workspace or user gives another", () => {
    assert.equal(namespaceSessionFlowType("Agent", base), namespaceSessionFlowType("Agent", { ...base }));
    assert.match(namespaceSessionFlowType("Agent", base), /^Agent-[0-9a-f]{12}$/);
    assert.notEqual(namespaceSessionFlowType("Agent", base), namespaceSessionFlowType("Agent", { ...base, workspace: "/home/a/other" }));
    assert.notEqual(namespaceSessionFlowType("Agent", base), namespaceSessionFlowType("Agent", { ...base, userId: "502" }));
  });

  test("two homes running on the defaults get different types", () => {
    const first = { ...base, configIdentity: "defaults", workspace: "/home/a/dex-agent-workspace" };
    const second = { ...base, userId: "502", configIdentity: "defaults", workspace: "/home/b/dex-agent-workspace" };
    assert.notEqual(namespaceSessionFlowType("Agent", first), namespaceSessionFlowType("Agent", second));
    assert.notEqual(namespaceSessionFlowType("Agent", first), namespaceSessionFlowType("Agent", { ...first, userId: "502" }));
  });
});

describe("local tool policies", () => {
  const turnConfig = resolveConfig({ approval: { always: ["read_file"] } }, "builtin", "m");
  function turnWith(toolPolicies: Record<string, ToolPolicy>): AgentTurnContext {
    return { toolPolicies } as unknown as AgentTurnContext;
  }

  test("an explicit policy can raise a local tool's effect and approval but never lower them", () => {
    assert.deepEqual(
      resolveTurnToolPolicy(turnWith({ run_shell_command: { effect: "read" } }), "run_shell_command", "non_idempotent_write", false, turnConfig),
      { effect: "non_idempotent_write", requiresApproval: true },
    );
    assert.deepEqual(
      resolveTurnToolPolicy(
        turnWith({ browser_click: { effect: "read", requiresApproval: false } }),
        "browser_click",
        "non_idempotent_write",
        true,
        turnConfig,
      ),
      { effect: "non_idempotent_write", requiresApproval: true },
    );
    assert.deepEqual(
      resolveTurnToolPolicy(turnWith({ fetch_web_page: { effect: "non_idempotent_write" } }), "fetch_web_page", "read", false, turnConfig),
      { effect: "non_idempotent_write", requiresApproval: true },
    );
    assert.deepEqual(
      resolveTurnToolPolicy(
        turnWith({ fetch_web_page: { effect: "non_idempotent_write", requiresApproval: false } }),
        "fetch_web_page",
        "read",
        false,
        turnConfig,
      ),
      { effect: "non_idempotent_write", requiresApproval: false },
    );
    assert.deepEqual(
      resolveTurnToolPolicy(
        turnWith({ run_shell_command: { effect: "read", requiresApproval: false } }),
        "run_shell_command",
        "non_idempotent_write",
        false,
        turnConfig,
      ),
      { effect: "non_idempotent_write", requiresApproval: false },
    );
    assert.deepEqual(
      resolveTurnToolPolicy(
        turnWith({ fetch_web_page: { effect: "read", requiresApproval: true } }),
        "fetch_web_page",
        "read",
        false,
        turnConfig,
      ),
      { effect: "read", requiresApproval: true },
    );
    assert.deepEqual(resolveTurnToolPolicy(turnWith({}), "write_file", "idempotent_write", true, turnConfig), {
      effect: "idempotent_write",
      requiresApproval: true,
    });
  });

  test("approval.always wins over an explicit policy", () => {
    assert.deepEqual(
      resolveTurnToolPolicy(turnWith({ read_file: { effect: "read", requiresApproval: false } }), "read_file", "read", false, turnConfig),
      { effect: "read", requiresApproval: true },
    );
  });
});
