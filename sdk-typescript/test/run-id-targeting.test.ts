// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Server, ServerCredentials, type sendUnaryData } from "@grpc/grpc-js";

import {
  Attribute,
  Client,
  Registry,
  StepList,
  rpc,
  stringCodec,
  type BlobCache,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "../src/index.js";
import {
  FlowServiceService,
  FlowStatus as ProtoFlowStatus,
  type FlowResult as ProtoFlowResult,
  type FlowServiceServer,
  type GetAttributesRequest,
  type GetAttributesResponse,
  type GetFlowSummaryRequest,
  type GetFlowSummaryResponse,
  type InvokeRPCRequest,
  type InvokeRPCResponse,
  type ServerInfo,
  type StopFlowRequest,
  type WaitForFlowRequest,
} from "../src/gen/dex.js";

const trackedRunId = "0b6f3c2e-7d4a-4f8e-9a51-2c3d4e5f6a7b";
const shipmentOutcome = new Attribute("shipment-outcome", stringCodec);

class RunIdStart implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RunIdStart";
  }

  public execute(_context: Context, _input: string): StepDecision {
    return { kind: "deadEnd" };
  }
}

class RunIdShipmentFlow implements Flow<string> {
  public getFlowType(): string {
    return "RunIdShipmentFlow";
  }

  public getSteps() {
    return StepList.startStep(new RunIdStart());
  }

  public getPersistenceSchema() {
    return { attributes: [shipmentOutcome] };
  }

  @rpc()
  public pingShipment(_context: Context): void {}
}

interface RecordedRequests {
  readonly summaries: GetFlowSummaryRequest[];
  readonly attributes: GetAttributesRequest[];
  readonly waits: WaitForFlowRequest[];
  readonly stops: StopFlowRequest[];
  readonly rpcs: InvokeRPCRequest[];
}

test("Client sends the run ID of each run-targeted call and an empty run ID by default", async () => {
  await withFakeService(async (client, flow, recorded) => {
    await client.describeFlow("shipment-42");
    const info = await client.describeFlow("shipment-42", trackedRunId);
    assert.equal(info.runId, trackedRunId);
    assert.deepEqual(recorded.summaries.map((request) => request.runId), ["", trackedRunId]);

    await client.getAttributes("shipment-42", shipmentOutcome);
    const [outcome] = await client.getFlowRunAttributes("shipment-42", trackedRunId, shipmentOutcome);
    assert.equal(outcome, "delivered");
    assert.deepEqual(recorded.attributes.map((request) => request.runId), ["", trackedRunId]);

    await client.waitForFlow("shipment-42");
    await client.waitForFlow("shipment-42", undefined, trackedRunId);
    assert.deepEqual(recorded.waits.map((request) => request.runId), ["", trackedRunId]);

    await client.stopFlow("shipment-42");
    await client.stopFlow("shipment-42", { runId: trackedRunId });
    assert.deepEqual(recorded.stops.map((request) => request.runId), ["", trackedRunId]);

    await client.invokeRPC(flow.pingShipment, "shipment-42");
    await client.invokeRPC(flow.pingShipment, "shipment-42", trackedRunId);
    await client.invokeRPCWithOptions(flow.pingShipment, "shipment-42", {}, trackedRunId);
    assert.deepEqual(recorded.rpcs.map((request) => request.runId), ["", trackedRunId, trackedRunId]);
  });
});

test("getFlowRunAttributes rejects an empty run ID before contacting the server", async () => {
  await withFakeService(async (client, _flow, recorded) => {
    await assert.rejects(
      client.getFlowRunAttributes("shipment-42", " ", shipmentOutcome),
      (failure: unknown) =>
        failure instanceof TypeError && failure.message.includes("requires a non-empty run ID"),
    );
    assert.deepEqual(recorded.attributes, []);
  });
});

async function withFakeService(
  run: (client: Client, flow: RunIdShipmentFlow, recorded: RecordedRequests) => Promise<void>,
): Promise<void> {
  const recorded: RecordedRequests = { summaries: [], attributes: [], waits: [], stops: [], rpcs: [] };
  const server = new Server();
  server.addService(FlowServiceService, {
    getServerInfo(_call, callback: sendUnaryData<ServerInfo>) {
      callback(null, { serverVersion: "test", minimumSupportedProtocolVersion: 1, currentProtocolVersion: 4, serverEngine: 0 });
    },
    getFlowSummary(call, callback: sendUnaryData<GetFlowSummaryResponse>) {
      const request = call.request as GetFlowSummaryRequest;
      recorded.summaries.push(request);
      callback(null, {
        flowRunId: { flowId: request.flowId, runId: request.runId || "latest-run" },
        firstRunId: "",
        requestId: "",
        flowType: "RunIdShipmentFlow",
        flowStatus: ProtoFlowStatus.FLOW_STATUS_COMPLETED,
        startTime: new Date(0),
        closeTime: undefined,
        runAttempt: 1,
      } as GetFlowSummaryResponse);
    },
    getAttributes(call, callback: sendUnaryData<GetAttributesResponse>) {
      recorded.attributes.push(call.request as GetAttributesRequest);
      callback(null, {
        attributes: [{ key: shipmentOutcome.name, value: { kind: { $case: "stringValue", value: "delivered" } } }],
      } as GetAttributesResponse);
    },
    waitForFlow(call, callback: sendUnaryData<ProtoFlowResult>) {
      recorded.waits.push(call.request as WaitForFlowRequest);
      callback(null, {
        flowStatus: ProtoFlowStatus.FLOW_STATUS_COMPLETED,
        results: [],
        errorType: 0,
        errorMessage: "",
      });
    },
    stopFlow(call, callback) {
      recorded.stops.push(call.request as StopFlowRequest);
      callback(null, {});
    },
    invokeRpc(call, callback: sendUnaryData<InvokeRPCResponse>) {
      recorded.rpcs.push(call.request as InvokeRPCRequest);
      callback(null, { output: undefined });
    },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (error, boundPort) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve(boundPort);
    });
  });
  const flow = new RunIdShipmentFlow();
  const client = new Client(new Registry([flow]), new MemoryBlobCache(), { serverAddress: `127.0.0.1:${port}` });
  try {
    await run(client, flow, recorded);
  } finally {
    await client.close();
    await new Promise<void>((resolve) => server.tryShutdown(() => resolve()));
  }
}

class MemoryBlobCache implements BlobCache {
  public readonly config = { directory: "memory", maxBytes: 1_024 };

  public get(_blobId: string): Uint8Array | undefined {
    return undefined;
  }

  public put(_blobId: string, _payload: Uint8Array): boolean {
    return true;
  }

  public delete(_blobId: string): void {}

  public deleteAll(): void {}

  public close(): void {}
}
