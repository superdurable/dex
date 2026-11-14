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

import { mergeInitDocument } from "../../src/config-file.js";

interface MergeCase {
  readonly name: string;
  readonly existing: Record<string, unknown>;
  readonly init: Record<string, unknown>;
  readonly expected: Record<string, unknown>;
}

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_init.json"), "utf8")) as {
  readonly merge_cases: readonly MergeCase[];
};

describe("cli_init.json merge cases", () => {
  for (const merge of fixture.merge_cases) {
    test(merge.name, () => {
      assert.deepEqual(mergeInitDocument(merge.existing, merge.init), merge.expected);
    });
  }
});
