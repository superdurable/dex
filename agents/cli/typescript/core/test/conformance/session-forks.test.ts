// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The CLI lines and task queues of agents/conformance/session_fork.json; the twin of the Python CLI core's
// tests/test_session_forks.py.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  chatForkFailureLine,
  chatForkUsageLine,
  forkTaskQueue,
  forkUsageError,
  forkedLine,
  missingSessionError,
  newCliSessionId,
  openFailureError,
  openPrompt,
  otherHarnessForkError,
  parseForkMessageNumber,
  resumeHint,
  unsupportedServerError,
} from "../../src/session-forks.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));

interface SessionForkCliFixture {
  readonly cli: Readonly<Record<string, string>> & {
    readonly task_queues: readonly {
      readonly name: string;
      readonly source_flow_type: string;
      readonly harness_flow_type: string;
      readonly session_id: string;
      readonly expected_task_queue: string;
    }[];
    readonly lines: readonly {
      readonly template: string;
      readonly values: Readonly<Record<string, string | number>>;
      readonly line: string;
    }[];
  };
}

const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "session_fork.json"), "utf8"),
) as SessionForkCliFixture;
const commandName = "dex-agents-scripted";

function format(template: string | undefined, values: Readonly<Record<string, string | number>>): string {
  return (template ?? "").replace(/\{(\w+)\}/g, (_, name: string) => String(values[name]));
}

describe("session fork CLI lines", () => {
  test("the lines match the shared templates", () => {
    const values = {
      source_session_id: "a-1",
      after_message: 4,
      session_id: "b-2",
      command_name: commandName,
      flow_type: "Other",
      error: "no",
    };
    const rendered: Record<string, string> = {
      forked: forkedLine("a-1", 4, "b-2"),
      open_prompt: openPrompt("b-2"),
      resume_hint: resumeHint(commandName, "b-2"),
      usage: forkUsageError(commandName),
      chat_usage: chatForkUsageLine,
      chat_failure: chatForkFailureLine("no"),
      unsupported_server: unsupportedServerError(commandName),
      other_harness: otherHarnessForkError("b-2", "Other", commandName),
      missing_session: missingSessionError("b-2", commandName),
      open_failure: openFailureError("b-2", "no"),
    };
    for (const [name, line] of Object.entries(rendered)) {
      assert.equal(line, format(fixture.cli[name], values), name);
    }
    for (const line of fixture.cli.lines) {
      assert.equal(format(fixture.cli[line.template], line.values), line.line);
    }
  });

  for (const testCase of fixture.cli.task_queues) {
    test(testCase.name, () => {
      assert.equal(
        forkTaskQueue(testCase.source_flow_type, testCase.harness_flow_type, testCase.session_id),
        testCase.expected_task_queue,
      );
    });
  }

  test("a new session ID has the CLI format", () => {
    assert.match(newCliSessionId("builtin"), /^builtin-\d{8}-\d{6}-[0-9a-f]{6}$/);
  });

  test("a message number is decimal, and an empty one means the current state", () => {
    assert.equal(parseForkMessageNumber(" 12 "), 12);
    assert.equal(parseForkMessageNumber(""), null);
    assert.equal(parseForkMessageNumber("two"), undefined);
    assert.equal(parseForkMessageNumber("-1"), undefined);
  });
});
