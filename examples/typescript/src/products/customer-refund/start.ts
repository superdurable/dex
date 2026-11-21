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

/** Starts one CustomerRefundFlow: start.js FLOW_ID [normal|fail-then-succeed|deny|small]. */

import { Client, FlowAlreadyStartedError, Registry } from "@superdurable/dex";

import {
  REFUND_SCENARIOS,
  newPayloadCodec,
  newRefundFlow,
  openRefundBlobCache,
  readRefundEnvironment,
  type RefundScenario,
} from "./refund-settings.js";

const [flowId, scenario = "normal"] = process.argv.slice(2);
if (flowId === undefined || !Object.hasOwn(REFUND_SCENARIOS, scenario)) {
  console.error(`usage: start.js FLOW_ID [${Object.keys(REFUND_SCENARIOS).join("|")}]`);
  process.exit(2);
}

const environment = readRefundEnvironment();
const refundFlow = newRefundFlow();
const cache = openRefundBlobCache(".dex-blob-cache/customer-refund-client");
const payloadCodec = await newPayloadCodec(environment.payloadKeyFile);
const client = new Client(new Registry([refundFlow]), cache, {
  serverAddress: environment.serverAddress,
  payloadCodec,
});
try {
  await client.startFlow(refundFlow, flowId, REFUND_SCENARIOS[scenario as RefundScenario]);
  console.log(`Started refund ${flowId} (${scenario}).`);
} catch (failure) {
  if (!(failure instanceof FlowAlreadyStartedError)) {
    throw failure;
  }
  console.error(`Flow ID ${flowId} is already used; choose another one.`);
  process.exitCode = 1;
} finally {
  await client.close();
  cache.close();
}
