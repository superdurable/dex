// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { namespaceSessionFlowType, sessionActionPermission } from "../../src/index.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_session_flow_types.json"), "utf8"),
) as SessionFlowTypeFixture;

interface SessionFlowTypeFixture {
  readonly harness_flow_type: string;
  readonly session_action_permission: string;
  readonly cases: readonly {
    readonly name: string;
    readonly host_name: string;
    readonly user_id: string;
    readonly config_identity: string;
    readonly workspace: string;
    readonly flow_type: string;
  }[];
}

describe("CLI session Flow type conformance", () => {
  test("the session Action permission matches every CLI core's", () => {
    assert.equal(sessionActionPermission, fixture.session_action_permission);
  });

  for (const testCase of fixture.cases) {
    test(`session Flow type: ${testCase.name}`, () => {
      const identity = {
        hostName: testCase.host_name,
        userId: testCase.user_id,
        configIdentity: testCase.config_identity,
        workspace: testCase.workspace,
      };
      assert.equal(namespaceSessionFlowType(fixture.harness_flow_type, identity), testCase.flow_type);
    });
  }
});
