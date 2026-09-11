// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  InitialAttribute,
  stringCodec,
  type Client,
} from "../../src/index.js";

import * as flows from "./iwf_flows.js";
export async function compilePersistenceReads(client: Client): Promise<void> {
  const flow = flows.BASIC_PERSISTENCE;
  await client.startFlow(flow, "persistence", "input", {
    attributes: [InitialAttribute.of(flow.initial, "initial")],
  });
  const output: string = await client.waitForFlow("persistence").then((result) => result.singleOutput(stringCodec));
  void output;
}

export async function compilePersistenceWrites(client: Client): Promise<void> {
  const flow = flows.SET_ATTRIBUTES;
  await client.startFlow(flow, "set-attributes", "input");
  await client.invokeRPC(flow.setData, "set-attributes", "value");
  await client.invokeRPC(flow.setMapOne, "set-attributes", "value");
  await client.invokeRPC(flow.setIndexed, "set-attributes");
  await client.invokeRPC(flow.complete, "set-attributes");
  const output: string = await client.waitForFlow("set-attributes").then((result) => result.singleOutput(stringCodec));
  void output;
}
