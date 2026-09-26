/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  canonicalEmailAddress,
  emailPartition,
} from "../../src/patterns/hash-partitioned-attribute-map/customer-directory-flow.js";
import {
  CURRENT_CHUNK_INSTANCE,
  validateSubscriberPageToken,
} from "../../src/patterns/sequentially-chunked-attribute-map/chunked-subscriber-flow.js";

describe("AttributeMap partition patterns", () => {
  it("uses the shared email partition golden vectors", () => {
    assert.deepEqual(emailPartition(" Alice@Example.COM "), {
      canonicalEmail: "alice@example.com",
      hash: 2493822278,
      partitionName: "partition-278",
    });
    assert.deepEqual(emailPartition("bob@example.com"), {
      canonicalEmail: "bob@example.com",
      hash: 3055529145,
      partitionName: "partition-145",
    });
    assert.deepEqual(emailPartition("support+west@example.org"), {
      canonicalEmail: "support+west@example.org",
      hash: 2156001632,
      partitionName: "partition-632",
    });
  });

  it("rejects invalid email addresses", () => {
    for (const emailAddress of ["", " \t\r\n", "josé@example.com"]) {
      assert.throws(() => canonicalEmailAddress(emailAddress));
    }
  });

  it("validates subscriber page tokens", () => {
    assert.equal(validateSubscriberPageToken(""), CURRENT_CHUNK_INSTANCE);
    assert.equal(
      validateSubscriberPageToken("00000000000000000101"),
      "00000000000000000101",
    );
    for (const pageToken of ["1", "archive", "00000000000000000002"]) {
      assert.throws(() => validateSubscriberPageToken(pageToken));
    }
  });
});
