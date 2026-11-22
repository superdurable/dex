/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";
import test from "node:test";

import type { FlowStatus } from "@superdurable/dex";

import { flowStatusName } from "../../src/products/customer-refund/controller.js";

test("refund describe uses the shared Flow status names", () => {
  assert.equal(flowStatusName("running"), "running");
  assert.equal(flowStatusName("completed"), "completed");
  assert.equal(flowStatusName("failed"), "failed");
  assert.equal(flowStatusName("cancelled"), "canceled");
  assert.equal(flowStatusName("terminated"), "terminated");
  assert.equal(flowStatusName("serverSideTimeoutInternalOnly"), "timeout");
  assert.equal(flowStatusName("unspecified" as FlowStatus), "unknown");
  assert.equal(flowStatusName("unexpected" as FlowStatus), "unknown");
});
