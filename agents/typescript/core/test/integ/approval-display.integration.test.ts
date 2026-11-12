// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The approval display rules of agents/conformance/approval_display.json; the twin of the Python core's
// tests/test_approval_display.py.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { boundApprovalDisplay } from "../../src/agent-approval-display.js";
import {
  buildConnectorToolApprovalDisplay,
  isCredentialLikeName,
  maxApprovalPreviewBytes,
  maxApprovalSummaryBytes,
  maxApprovalTargetBytes,
  redactInlineCredentials,
} from "../../src/index.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";

interface ApprovalDisplayCases {
  readonly credential_names: { readonly credential: readonly string[]; readonly not_credential: readonly string[] };
  readonly inline_credentials: readonly { readonly text: string; readonly redacted: string }[];
  readonly connector_displays: readonly {
    readonly connector_id: string;
    readonly connection_name: string;
    readonly operation: string;
    readonly arguments: string;
    readonly target: string;
    readonly summary: string;
  }[];
}

const cases = readConformanceJson<ApprovalDisplayCases>("approval_display.json");

describe("approval displays follow the shared conformance cases", () => {
  test("credential names are found as the conformance suite says", () => {
    for (const name of cases.credential_names.credential) {
      assert.equal(isCredentialLikeName(name), true, name);
    }
    for (const name of cases.credential_names.not_credential) {
      assert.equal(isCredentialLikeName(name), false, name);
    }
  });

  test("inline credentials are redacted as the conformance suite says", () => {
    for (const { text, redacted } of cases.inline_credentials) {
      assert.equal(redactInlineCredentials(text), redacted, text);
    }
  });

  test("connector displays have the conformance suite's target and summary", () => {
    for (const expected of cases.connector_displays) {
      const display = buildConnectorToolApprovalDisplay({
        connectorId: expected.connector_id,
        connectionName: expected.connection_name,
        operation: expected.operation,
        arguments: expected.arguments,
      });
      assert.deepEqual(display, { target: expected.target, summary: expected.summary, preview: null });
    }
  });

  test("the session cuts a long target and summary at whole characters and drops a long preview", () => {
    const bounded = boundApprovalDisplay({
      target: "é".repeat(maxApprovalTargetBytes),
      summary: `Runs a shell command in /work: ${"x".repeat(9 * 1024 * 1024)}`,
      preview: "p".repeat(maxApprovalPreviewBytes + 1),
    });
    assert.ok(bounded !== null);
    assert.equal(bounded.preview, null);
    assert.ok(bounded.target.endsWith("…"));
    assert.ok(Buffer.byteLength(bounded.target, "utf8") <= maxApprovalTargetBytes);
    assert.equal(bounded.target, `${"é".repeat(Math.floor((maxApprovalTargetBytes - 3) / 2))}…`);
    assert.ok(bounded.summary.startsWith("Runs a shell command in /work: xxx"));
    assert.equal(Buffer.byteLength(bounded.summary, "utf8"), maxApprovalSummaryBytes);
    assert.deepEqual(boundApprovalDisplay({ target: "t", summary: "s" }), { target: "t", summary: "s", preview: null });
    assert.equal(boundApprovalDisplay({ target: 1, summary: "s" }), null);
  });
});
