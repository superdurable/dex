// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The CLI cases of agents/conformance/approval_display.json; the twin of the Python CLI core's
// tests/test_approval_display_conformance.py.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  buildFilePreview,
  buildShellCommandDisplay,
  escapeControlCharacters,
  isCredentialFileName,
} from "../../src/local-tools/approval-display.js";

interface CliApprovalDisplayCases {
  readonly cli: {
    readonly credential_files: { readonly credential: readonly string[]; readonly not_credential: readonly string[] };
    readonly file_previews: readonly {
      readonly path: string;
      readonly current: string | null;
      readonly new: string;
      readonly preview: string | null;
    }[];
    readonly shell_displays: readonly {
      readonly workspace: string;
      readonly command: string;
      readonly target: string;
      readonly summary: string;
    }[];
    readonly escaped_paths: readonly { readonly text: string; readonly escaped: string }[];
  };
}

const agentsDirectory = fileURLToPath(new URL("../../../../../../", import.meta.url));
const { cli } = JSON.parse(
  readFileSync(join(agentsDirectory, "conformance", "approval_display.json"), "utf8"),
) as CliApprovalDisplayCases;

describe("CLI approval display conformance", () => {
  test("credential files are found as the conformance suite says", () => {
    for (const path of cli.credential_files.credential) {
      assert.equal(isCredentialFileName(path), true, path);
    }
    for (const path of cli.credential_files.not_credential) {
      assert.equal(isCredentialFileName(path), false, path);
    }
  });

  test("file write previews are the conformance suite's diffs", () => {
    for (const expected of cli.file_previews) {
      assert.equal(buildFilePreview(expected.path, expected.current, expected.new), expected.preview, expected.path);
    }
  });

  test("shell command displays redact inline credentials as the conformance suite says", () => {
    for (const expected of cli.shell_displays) {
      assert.deepEqual(buildShellCommandDisplay(expected.workspace, expected.command), {
        target: expected.target,
        summary: expected.summary,
        preview: null,
      });
    }
  });

  test("control characters in paths are escaped as the conformance suite says", () => {
    for (const expected of cli.escaped_paths) {
      assert.equal(escapeControlCharacters(expected.text), expected.escaped);
    }
  });
});
