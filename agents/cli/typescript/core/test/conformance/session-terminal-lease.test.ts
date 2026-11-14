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

import {
  animatedWaitingLine,
  cutToColumns,
  defaultSessionLeaseTtlSeconds,
  defaultWaitingColumns,
  formatLeaseHolder,
  formatLeaseSince,
  leaseLostLine,
  leaseUnconfirmedLine,
  plainWaitingLine,
  readSessionLeaseTtlSeconds,
  renewalSafetyMarginRatio,
  tookOverLine,
  waitingFrameIntervalMs,
  waitingRedrawPrefix,
  waitingRetryIntervalMs,
  waitingSpinnerFrames,
} from "../../src/session-terminal-lease.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_session_lease.json"), "utf8"),
) as SessionLeaseFixture;

interface HolderCase {
  readonly name: string;
  readonly session_id: string;
  readonly host_name: string;
  readonly process_id: number;
  readonly since: string;
  readonly line: string;
}

interface SessionLeaseFixture {
  readonly spinner_frames: readonly string[];
  readonly frame_interval_ms: number;
  readonly retry_interval_ms: number;
  readonly redraw_prefix: string;
  readonly default_columns: number;
  readonly interrupted_exit_code: number;
  readonly lease_lost_exit_code: number;
  readonly animated_waiting_cases: readonly (HolderCase & {
    readonly elapsed_seconds: number;
    readonly frame_index: number;
    readonly columns: number;
  })[];
  readonly plain_waiting_cases: readonly HolderCase[];
  readonly took_over_cases: readonly { readonly name: string; readonly session_id: string; readonly line: string }[];
  readonly lease_lost_cases: readonly HolderCase[];
  readonly renewal_safety_margin_ratio: number;
  readonly lease_unconfirmed_exit_code: number;
  readonly lease_unconfirmed_cases: readonly { readonly name: string; readonly session_id: string; readonly line: string }[];
  readonly since_cases: readonly {
    readonly name: string;
    readonly opened_at: string;
    readonly time_zone: string;
    readonly since: string;
  }[];
}

describe("session terminal lease lines: the shared cases", () => {
  test("the unconfirmed lease line and its safety margin", () => {
    assert.equal(renewalSafetyMarginRatio, fixture.renewal_safety_margin_ratio);
    assert.equal(fixture.lease_unconfirmed_exit_code, 1);
    for (const testCase of fixture.lease_unconfirmed_cases) {
      assert.equal(leaseUnconfirmedLine(testCase.session_id), testCase.line, testCase.name);
    }
  });

  test("the spinner, timings, redraw prefix, and default width match", () => {
    assert.deepEqual(waitingSpinnerFrames, fixture.spinner_frames);
    assert.equal(waitingFrameIntervalMs, fixture.frame_interval_ms);
    assert.equal(waitingRetryIntervalMs, fixture.retry_interval_ms);
    assert.equal(waitingRedrawPrefix, fixture.redraw_prefix);
    assert.equal(defaultWaitingColumns, fixture.default_columns);
  });

  test("the animated waiting line, cut to the terminal width", () => {
    for (const testCase of fixture.animated_waiting_cases) {
      const holder = formatLeaseHolder(testCase.host_name, testCase.process_id, testCase.since);
      const line = animatedWaitingLine(testCase.frame_index, testCase.session_id, holder, testCase.elapsed_seconds);
      assert.equal(cutToColumns(line, testCase.columns), testCase.line, testCase.name);
    }
  });

  test("the plain waiting, took over, and lease lost lines", () => {
    for (const testCase of fixture.plain_waiting_cases) {
      const holder = formatLeaseHolder(testCase.host_name, testCase.process_id, testCase.since);
      assert.equal(plainWaitingLine(testCase.session_id, holder), testCase.line, testCase.name);
    }
    for (const testCase of fixture.took_over_cases) {
      assert.equal(tookOverLine(testCase.session_id), testCase.line, testCase.name);
    }
    for (const testCase of fixture.lease_lost_cases) {
      const holder = formatLeaseHolder(testCase.host_name, testCase.process_id, testCase.since);
      assert.equal(leaseLostLine(testCase.session_id, holder), testCase.line, testCase.name);
    }
  });

  test("since is the HH:MM of the lease's opening time in the time zone", () => {
    for (const testCase of fixture.since_cases) {
      assert.equal(formatLeaseSince(testCase.opened_at, testCase.time_zone), testCase.since, testCase.name);
    }
  });

  test("the TTL override takes an integer from 1 to 3600 and ignores anything else", () => {
    assert.equal(readSessionLeaseTtlSeconds(undefined), defaultSessionLeaseTtlSeconds);
    assert.equal(readSessionLeaseTtlSeconds("3"), 3);
    assert.equal(readSessionLeaseTtlSeconds(" 3600 "), 3600);
    for (const ignored of ["", "0", "3601", "2.5", "-1", "ten"]) {
      assert.equal(readSessionLeaseTtlSeconds(ignored), defaultSessionLeaseTtlSeconds, ignored);
    }
  });
});
