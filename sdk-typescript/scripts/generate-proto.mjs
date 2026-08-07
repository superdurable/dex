// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

const project = resolve(import.meta.dirname, "..");
const protoc = process.env.PROTOC ?? "protoc";
const protobufInclude = resolveProtobufInclude(protoc);
const output = join(project, "src", "gen");
mkdirSync(output, { recursive: true });

const result = spawnSync(
  protoc,
  [
    `-I${resolve(project, "../protos")}`,
    `-I${protobufInclude}`,
    `--plugin=protoc-gen-ts_proto=${join(project, "node_modules/.bin/protoc-gen-ts_proto")}`,
    `--ts_proto_out=${output}`,
    "--ts_proto_opt=outputServices=grpc-js,forceLong=bigint,oneof=unions-value,esModuleInterop=true,importSuffix=.js,useDate=true,outputJsonMethods=false",
    resolve(project, "../protos/dex.proto"),
    join(protobufInclude, "google/protobuf/empty.proto"),
    join(protobufInclude, "google/protobuf/duration.proto"),
    join(protobufInclude, "google/protobuf/struct.proto"),
    join(protobufInclude, "google/protobuf/timestamp.proto"),
  ],
  { stdio: "inherit" },
);

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

function resolveProtobufInclude(protocPath) {
  const candidates = [
    process.env.PROTOC_INCLUDE,
    protocPath.includes("/") ? resolve(dirname(protocPath), "../include") : undefined,
    "/usr/local/include",
    "/usr/include",
    "/opt/homebrew/include",
  ];
  for (const candidate of candidates) {
    if (candidate !== undefined && existsSync(join(candidate, "google/protobuf/empty.proto"))) {
      return candidate;
    }
  }
  throw new Error("set PROTOC_INCLUDE to the directory containing google/protobuf");
}
