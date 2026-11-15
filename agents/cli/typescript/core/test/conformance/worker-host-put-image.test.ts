// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, describe, test } from "node:test";

import type { OpenedSessionWorker } from "../../src/agent-cli.js";
import { executeWorkerHostMethod, type WorkerHostMethodContext } from "../../src/worker-host/worker-host-methods.js";

/** A 1x1 PNG. */
const png = Buffer.from(
  "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d49444154789c63600000000200015" +
    "4a24f5d0000000049454e44ae426082",
  "hex",
);

const directory = mkdtempSync(join(tmpdir(), "put-image-"));
after(() => rmSync(directory, { recursive: true, force: true }));

/** A Worker host whose media store fails every write with `failure`. */
function failingStoreContext(failure: Error, logLines: string[]): WorkerHostMethodContext {
  const opened = {
    mediaStore: {
      putImage: () => Promise.reject(failure),
    },
  } as unknown as OpenedSessionWorker;
  return { harnessName: "test", opened, connectorsEnabled: null, log: (line) => logLines.push(line) };
}

describe("put_image failures say why", () => {
  test("a media store failure returns and logs its reason", async () => {
    const path = join(directory, "dot.png");
    writeFileSync(path, png);
    const logLines: string[] = [];
    const outcome = await executeWorkerHostMethod("put_image", { path }, "key-1", failingStoreContext(new Error("disk full"), logLines));
    assert.deepEqual(outcome, {
      error: { code: "store_failed", message: "dot.png could not be stored in the media store: disk full" },
    });
    assert.deepEqual(logLines, ["put_image could not store dot.png: disk full"]);
  });

  test("an unreadable file is not reported as missing", { skip: process.getuid?.() === 0 }, async () => {
    const path = join(directory, "locked.png");
    writeFileSync(path, png);
    chmodSync(path, 0o000);
    const outcome = await executeWorkerHostMethod("put_image", { path }, "key-2", failingStoreContext(new Error("unused"), []));
    assert.deepEqual(outcome, { error: { code: "not_found", message: "locked.png cannot be read: permission denied (EACCES)." } });
  });

  test("a missing file and an oversized file say so", async () => {
    const missing = await executeWorkerHostMethod("put_image", { path: join(directory, "missing.png") }, "key-3", failingStoreContext(new Error("unused"), []));
    assert.deepEqual(missing, { error: { code: "not_found", message: "missing.png does not exist." } });
    const big = join(directory, "big.png");
    writeFileSync(big, Buffer.alloc(3_750_001));
    const oversized = await executeWorkerHostMethod("put_image", { path: big }, "key-4", failingStoreContext(new Error("unused"), []));
    assert.deepEqual(oversized, {
      error: { code: "too_large", message: "big.png has 3,750,001 bytes; an image may have at most 3,750,000." },
    });
  });
});
