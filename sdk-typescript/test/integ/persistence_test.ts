// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
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
  const data: string | undefined = await client.getAttribute("persistence", flow.data);
  const integer: number | undefined = await client.getAttribute("persistence", flow.integer);
  const datetime: Date | undefined = await client.getAttribute("persistence", flow.datetime);
  void data;
  void integer;
  void datetime;
}

export async function compilePersistenceWrites(client: Client): Promise<void> {
  const flow = flows.SET_ATTRIBUTES;
  await client.startFlow(flow, "set-attributes", "input");
  await client.setAttribute("set-attributes", flow.data, "value");
  await client.setAttribute("set-attributes", flow.dataMap, "one", "value");
  await client.setAttribute("set-attributes", flow.keyword, "keyword");
  await client.setAttribute("set-attributes", flow.decimal, 1.5);
  await client.setAttribute("set-attributes", flow.integer, 1);
  await client.setAttribute("set-attributes", flow.bool, true);
  await client.setAttribute("set-attributes", flow.keywords, ["one", "two"]);
  const output: string = await client.waitForFlow("set-attributes").then((result) => result.singleOutput(stringCodec));
  void output;
}
