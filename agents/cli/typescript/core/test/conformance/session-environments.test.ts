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

import type { SessionTerminalLease } from "@superdurable/dex-agents";

import {
  cancelledTurnLine,
  isHarnessSessionFlowType,
  otherHarnessSessionError,
  refusedModelCallError,
  refusedToolCallError,
  sessionElsewhereReason,
  takeoverLine,
  turnElsewhereReason,
} from "../../src/session-environment-fence.js";
import {
  holderIdEnvironmentSeparator,
  isSessionCallAllowedHere,
  leaseHolderEnvironment,
  newSessionLeaseHolder,
} from "../../src/session-terminal-lease.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_session_environments.json"), "utf8"),
) as SessionEnvironmentsFixture;

interface SessionEnvironmentsFixture {
  readonly holder_id: {
    readonly separator: string;
    readonly random_hex_length: number;
    readonly environment_cases: readonly { readonly name: string; readonly holder_id: string; readonly environment: string | null }[];
  };
  readonly decision_cases: readonly {
    readonly name: string;
    readonly session_flow_type: string;
    readonly home_flow_type: string;
    readonly holder_id: string;
    readonly stored_holder_id: string | null;
    readonly acts: boolean;
  }[];
  readonly harness_flow_type_cases: readonly {
    readonly name: string;
    readonly harness_flow_type: string;
    readonly flow_type: string;
    readonly is_harness_session: boolean;
  }[];
  readonly line_cases: readonly {
    readonly name: string;
    readonly template: string;
    readonly values: Readonly<Record<string, string>>;
    readonly line: string;
  }[];
}

function storedLease(holderId: string | null): SessionTerminalLease | undefined {
  return holderId === null
    ? undefined
    : { holderId, hostName: "host-a", processId: 4242, openedAt: "2026-11-13T20:31:00Z", expiresAt: "2026-11-13T20:31:15Z" };
}

function renderLine(template: string, values: Readonly<Record<string, string>>): string {
  const value = (name: string): string => {
    const found = values[name];
    assert.ok(found !== undefined, `${template} needs ${name}`);
    return found;
  };
  switch (template) {
    case "takeover_line":
      return takeoverLine(value("session_id"), value("previous_environment"), value("this_environment"), value("workspace"));
    case "cancelled_turn_line":
      return cancelledTurnLine;
    case "refused_tool_call":
      return refusedToolCallError(value("tool_name"), value("reason"));
    case "refused_model_call":
      return refusedModelCallError(value("reason"));
    case "session_elsewhere_reason":
      return sessionElsewhereReason(value("session_id"));
    case "turn_elsewhere_reason":
      return turnElsewhereReason(value("session_id"));
    case "other_harness_error":
      return otherHarnessSessionError(value("session_id"), value("flow_type"), value("command_name"));
    default:
      throw new Error(`no case renders ${template}`);
  }
}

describe("sessions that move between execution environments: the shared cases", () => {
  test("a new holder ID names this environment, then 32 random hex digits", () => {
    assert.equal(holderIdEnvironmentSeparator, fixture.holder_id.separator);
    const holder = newSessionLeaseHolder(4242, "DexAgentsBuiltinCliSession-ab12cd");
    const [environment, random] = holder.holderId.split(fixture.holder_id.separator);
    assert.equal(environment, "DexAgentsBuiltinCliSession-ab12cd");
    assert.match(random ?? "", new RegExp(`^[0-9a-f]{${fixture.holder_id.random_hex_length}}$`));
    assert.equal(leaseHolderEnvironment(holder.holderId), "DexAgentsBuiltinCliSession-ab12cd");
  });

  test("the environment a holder ID names", () => {
    for (const testCase of fixture.holder_id.environment_cases) {
      assert.equal(leaseHolderEnvironment(testCase.holder_id) ?? null, testCase.environment, testCase.name);
    }
  });

  test("whether a Worker runs a session's calls", () => {
    for (const testCase of fixture.decision_cases) {
      const isAllowed = isSessionCallAllowedHere({
        sessionFlowType: testCase.session_flow_type,
        homeFlowType: testCase.home_flow_type,
        holderId: testCase.holder_id,
        stored: storedLease(testCase.stored_holder_id),
      });
      assert.equal(isAllowed, testCase.acts, testCase.name);
    }
  });

  test("which Flow types are this harness's sessions", () => {
    for (const testCase of fixture.harness_flow_type_cases) {
      assert.equal(isHarnessSessionFlowType(testCase.harness_flow_type, testCase.flow_type), testCase.is_harness_session, testCase.name);
    }
  });

  test("the lines and errors", () => {
    for (const testCase of fixture.line_cases) {
      assert.equal(renderLine(testCase.template, testCase.values), testCase.line, testCase.name);
    }
  });
});
