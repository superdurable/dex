// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { readFile } from "node:fs/promises";

const reportPath = process.argv[2] ?? "coverage/lcov.info";
const records = (await readFile(reportPath, "utf8")).split("end_of_record");
const uncovered = [];

for (const record of records) {
  const lines = record.trim().split("\n");
  const source = lines.find((line) => line.startsWith("SF:"))?.slice(3);
  if (source === undefined) {
    continue;
  }
  const missing = lines
    .filter((line) => line.startsWith("DA:"))
    .map((line) => line.slice(3).split(",").map(Number))
    .filter(([, hits]) => hits === 0)
    .map(([line]) => line)
    .filter((line) => line !== undefined);
  if (missing.length > 0) {
    uncovered.push(`${source}: ${ranges(missing).join(", ")}`);
  }
}

console.log("\nUncovered TypeScript lines:");
console.log(uncovered.length === 0 ? "none" : uncovered.join("\n"));

function ranges(lines) {
  const output = [];
  let start = lines[0];
  let end = start;
  for (const line of lines.slice(1)) {
    if (line === end + 1) {
      end = line;
      continue;
    }
    output.push(formatRange(start, end));
    start = line;
    end = line;
  }
  output.push(formatRange(start, end));
  return output;
}

function formatRange(start, end) {
  return start === end ? String(start) : `${start}-${end}`;
}
