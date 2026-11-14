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

import { cliAppWorkerId } from "../../src/session-opener.js";
import {
  cancelledTurnLine,
  describeSessionEnvironment,
  describeSessionEnvironmentIdentity,
  isLegacySessionFlowType,
  isOtherSessionEnvironment,
  namedTaskQueueServerError,
  otherEnvironmentLegacySessionError,
  otherHarnessSessionError,
  refusedModelCallError,
  refusedToolCallError,
  sessionTaskQueue,
  takenOverTurnReason,
  takeoverLine,
  turnNotCancelledLine,
} from "../../src/session-task-queues.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_session_task_queues.json"), "utf8"),
) as SessionTaskQueueFixture;

type Environment = Readonly<Record<string, string>>;

interface SessionTaskQueueFixture {
  readonly harness_flow_type: string;
  readonly task_queue_cases: readonly { readonly name: string; readonly session_id: string; readonly task_queue: string }[];
  readonly legacy_flow_type_cases: readonly { readonly name: string; readonly flow_type: string; readonly is_legacy: boolean }[];
  readonly environment_cases: readonly { readonly name: string; readonly environment: Environment; readonly text: string }[];
  readonly other_environment_cases: readonly {
    readonly name: string;
    readonly previous: Environment;
    readonly current: Environment;
    readonly is_other_environment: boolean;
  }[];
  readonly identity_cases: readonly { readonly name: string; readonly environment: Environment; readonly text: string }[];
  readonly templates: Readonly<Record<string, string>>;
  readonly template_cases: readonly {
    readonly template: string;
    readonly values: Readonly<Record<string, string>>;
    readonly line: string;
  }[];
  readonly app_worker_id_cases: readonly {
    readonly name: string;
    readonly harness_name: string;
    readonly host_name: string;
    readonly process_id: number;
    readonly suffix: string | null;
    readonly app_worker_id: string;
  }[];
}

const renderers: Readonly<Record<string, (values: Readonly<Record<string, string>>) => string>> = {
  takeover_line: (values) =>
    takeoverLine(values.session_id ?? "", values.previous_environment ?? "", values.environment ?? ""),
  taken_over_turn_reason: (values) => takenOverTurnReason(values.session_id ?? ""),
  refused_tool_call_error: (values) => refusedToolCallError(values.tool_name ?? "", values.reason ?? ""),
  refused_model_call_error: (values) => refusedModelCallError(values.reason ?? ""),
  other_environment_legacy_session_error: (values) =>
    otherEnvironmentLegacySessionError(values.session_id ?? "", values.flow_type ?? ""),
  other_harness_session_error: (values) =>
    otherHarnessSessionError(values.session_id ?? "", values.flow_type ?? "", values.command_name ?? ""),
  named_task_queue_server_error: (values) => namedTaskQueueServerError(values.command_name ?? ""),
};

describe("CLI session task queue conformance", () => {
  for (const testCase of fixture.task_queue_cases) {
    test(`session task queue: ${testCase.name}`, () => {
      assert.equal(sessionTaskQueue(testCase.session_id), testCase.task_queue);
    });
  }

  for (const testCase of fixture.legacy_flow_type_cases) {
    test(`legacy session Flow type: ${testCase.name}`, () => {
      assert.equal(isLegacySessionFlowType(fixture.harness_flow_type, testCase.flow_type), testCase.is_legacy);
    });
  }

  for (const testCase of fixture.environment_cases) {
    test(`session environment text: ${testCase.name}`, () => {
      assert.equal(describeSessionEnvironment(testCase.environment), testCase.text);
    });
  }

  for (const testCase of fixture.other_environment_cases) {
    test(`other environment: ${testCase.name}`, () => {
      assert.equal(isOtherSessionEnvironment(testCase.previous, testCase.current), testCase.is_other_environment);
    });
  }

  for (const testCase of fixture.identity_cases) {
    test(`environment identity text: ${testCase.name}`, () => {
      assert.equal(describeSessionEnvironmentIdentity(testCase.environment), testCase.text);
    });
  }

  test("every template case renders its line, and the cancelled turn line matches", () => {
    assert.equal(cancelledTurnLine, fixture.templates.cancelled_turn_line);
    assert.equal(turnNotCancelledLine, fixture.templates.turn_not_cancelled_line);
    for (const testCase of fixture.template_cases) {
      const render = renderers[testCase.template];
      assert.ok(render !== undefined, testCase.template);
      assert.equal(render(testCase.values), testCase.line, testCase.template);
    }
  });

  for (const testCase of fixture.app_worker_id_cases) {
    test(`app Worker ID: ${testCase.name}`, () => {
      const appWorkerId = cliAppWorkerId(
        testCase.harness_name,
        testCase.host_name,
        testCase.process_id,
        testCase.suffix ?? undefined,
      );
      assert.equal(appWorkerId, testCase.app_worker_id);
      assert.ok(appWorkerId.length <= 63);
    });
  }
});
