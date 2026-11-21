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

/** Approves or rejects a waiting refund: decide.js FLOW_ID approve|reject REASON. */

import {
  Client,
  FlowNotActiveOrNotFoundError,
  Registry,
  WorkerInvocationError,
} from "@superdurable/dex";

import type { DecisionReason } from "./refund-flow.js";
import {
  newPayloadCodec,
  newRefundFlow,
  openRefundBlobCache,
  readRefundEnvironment,
} from "./refund-settings.js";

const [flowId, decision, reason] = process.argv.slice(2);
if (flowId === undefined || (decision !== "approve" && decision !== "reject") || reason === undefined) {
  console.error("usage: decide.js FLOW_ID approve|reject REASON");
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
  const rpc = decision === "approve" ? refundFlow.approveRefund : refundFlow.rejectRefund;
  const input: DecisionReason = { reason };
  await client.invokeRPC(rpc, flowId, input);
  const outcome = decision === "approve" ? "approved" : "rejected";
  console.log(`Refund ${flowId}: ${outcome}.`);
} catch (failure) {
  if (failure instanceof FlowNotActiveOrNotFoundError) {
    console.error(`Refund ${flowId} is closed or does not exist.`);
  } else if (failure instanceof WorkerInvocationError) {
    console.error(`Refund ${flowId}: ${failure.message}.`);
  } else {
    throw failure;
  }
  process.exitCode = 1;
} finally {
  await client.close();
  cache.close();
}
