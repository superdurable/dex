// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";

import { status, type ServiceError } from "@grpc/grpc-js";

import {
  FlowDefinitionUploadOutcome,
  type FlowDefinitionDocument,
  type FlowDefinitionUploadResult,
} from "./flow-definition.js";
import {
  FlowDefinitionUploadOutcome as ProtoFlowDefinitionUploadOutcome,
  type UploadFlowDefinitionsRequest,
  type UploadFlowDefinitionsResponse,
} from "./gen/dex.js";

export const maxFlowDefinitionsPerUpload = 256;
export const maxFlowDefinitionUploadContentBytes = 16 * 1024 * 1024;

const rfc3339Pattern = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;
const jsonWhitespace = " \t\n\r";
const jsonScalarTerminators = ",}] \t\n\r";

export interface PreparedFlowDefinition {
  readonly flowType: string;
  readonly content: Uint8Array;
  readonly generatedAt: Date;
  readonly origin: string;
}

interface JsonObjectMember {
  readonly key: string;
  readonly valueStart: number;
  readonly valueEnd: number;
}

/**
 * Loads, validates, and merges the configured Flow definitions.
 * @returns One definition per Flow type: files in sorted path order, then in-memory documents.
 * @throws {Error} A file cannot be read or parsed, a document is invalid, or a Flow type repeats.
 */
export function prepareFlowDefinitions(
  paths: readonly string[],
  documents: readonly FlowDefinitionDocument[],
): PreparedFlowDefinition[] {
  const prepared = [
    ...flowDefinitionFiles(paths).map(loadFlowDefinitionFile),
    ...documents.map(prepareDocument),
  ];
  const originsByFlowType = new Map<string, string>();
  for (const definition of prepared) {
    const existingOrigin = originsByFlowType.get(definition.flowType);
    if (existingOrigin !== undefined) {
      throw new Error(
        `Flow definition for Flow type ${JSON.stringify(definition.flowType)} is configured twice: ${existingOrigin} and ${definition.origin}`,
      );
    }
    originsByFlowType.set(definition.flowType, definition.origin);
  }
  return prepared;
}

export function uploadRequests(
  definitions: readonly PreparedFlowDefinition[],
  uploader: string,
  isForced: boolean,
): { readonly request: UploadFlowDefinitionsRequest; readonly batch: readonly PreparedFlowDefinition[] }[] {
  const requests = [];
  for (const batch of flowDefinitionBatches(definitions)) {
    requests.push({
      request: {
        definitions: batch.map((definition) => ({
          flowType: definition.flowType,
          content: definition.content,
          generatedAt: definition.generatedAt,
        })),
        uploader,
        force: isForced,
      },
      batch,
    });
  }
  return requests;
}

/**
 * Splits definitions into upload batches in their given order.
 * @returns Batches of at most 256 definitions whose content totals at most 16 MiB; a larger single
 * definition forms its own batch.
 */
export function flowDefinitionBatches(
  definitions: readonly PreparedFlowDefinition[],
): (readonly PreparedFlowDefinition[])[] {
  const batches: (readonly PreparedFlowDefinition[])[] = [];
  let batchStart = 0;
  let batchBytes = 0;
  definitions.forEach((definition, index) => {
    const contentBytes = definition.content.byteLength;
    const isBatchFull = index - batchStart === maxFlowDefinitionsPerUpload
      || batchBytes + contentBytes > maxFlowDefinitionUploadContentBytes;
    if (index > batchStart && isBatchFull) {
      batches.push(definitions.slice(batchStart, index));
      batchStart = index;
      batchBytes = 0;
    }
    batchBytes += contentBytes;
  });
  if (batchStart < definitions.length) {
    batches.push(definitions.slice(batchStart));
  }
  return batches;
}

export function uploadResultsFromResponse(response: UploadFlowDefinitionsResponse): FlowDefinitionUploadResult[] {
  return response.results.map((result) => ({
    flowType: result.flowType,
    outcome: uploadOutcome(result.outcome),
    storedDigest: result.storedDigest,
    storedGeneratedAt: result.storedGeneratedAt ?? new Date(0),
  }));
}

export function flowDefinitionUploader(sdkVersion: string): string {
  return `sdk-typescript/${sdkVersion}`;
}

/** Uploads a Worker's Flow definitions, logging every failure instead of throwing. */
export async function uploadWorkerFlowDefinitions(
  upload: (request: UploadFlowDefinitionsRequest) => Promise<UploadFlowDefinitionsResponse>,
  definitions: readonly PreparedFlowDefinition[],
  registeredFlowTypes: ReadonlySet<string>,
  uploader: string,
): Promise<void> {
  const selected = definitions.filter((definition) => {
    if (registeredFlowTypes.has(definition.flowType)) return true;
    console.warn(
      `skipping Flow definition upload because this Worker does not register the Flow type: flowType=${definition.flowType} origin=${definition.origin}`,
    );
    return false;
  });
  for (const { request, batch } of uploadRequests(selected, uploader, false)) {
    let response: UploadFlowDefinitionsResponse;
    try {
      response = await upload(request);
    } catch (failure) {
      logWorkerUploadFailure(failure);
      return;
    }
    try {
      logWorkerUploadResults(batch, response);
    } catch (failure) {
      logWorkerUploadFailure(failure);
      return;
    }
  }
}

function logWorkerUploadFailure(failure: unknown): void {
  if ((failure as Partial<ServiceError> | undefined)?.code === status.UNIMPLEMENTED) {
    console.warn(
      "Dex Server does not support Flow definition upload; upgrade the Server to store Flow definitions. The Worker continues starting",
      failure,
    );
    return;
  }
  console.warn("Flow definition upload failed; the Worker continues starting", failure);
}

function logWorkerUploadResults(
  batch: readonly PreparedFlowDefinition[],
  response: UploadFlowDefinitionsResponse,
): void {
  const definitionsByFlowType = new Map(batch.map((definition) => [definition.flowType, definition]));
  for (const result of uploadResultsFromResponse(response)) {
    const definition = definitionsByFlowType.get(result.flowType);
    const origin = definition?.origin ?? "unknown";
    // Every upload log goes to stderr, so an unchanged definition stays silent on each restart.
    if (result.outcome === FlowDefinitionUploadOutcome.UNCHANGED) {
      continue;
    }
    if (result.outcome === FlowDefinitionUploadOutcome.KEPT_EXISTING) {
      const uploadedDigest = definition === undefined
        ? "unknown"
        : createHash("sha256").update(definition.content).digest("hex");
      console.warn(
        "Dex Server kept a different Flow definition generated at the same time or later; upload with force to replace it: "
          + `flowType=${result.flowType} origin=${origin} uploadedDigest=${uploadedDigest} `
          + `uploadedGeneratedAt=${definition?.generatedAt.toISOString() ?? "unknown"} `
          + `storedDigest=${result.storedDigest} storedGeneratedAt=${result.storedGeneratedAt.toISOString()}`,
      );
    } else if (result.outcome === FlowDefinitionUploadOutcome.UNSPECIFIED) {
      console.warn(
        `Dex Server returned an unknown Flow definition upload outcome: flowType=${result.flowType} origin=${origin} storedDigest=${result.storedDigest}`,
      );
    } else {
      console.warn(
        `Flow definition ${result.outcome}: flowType=${result.flowType} origin=${origin} digest=${result.storedDigest}`,
      );
    }
  }
}

function uploadOutcome(outcome: ProtoFlowDefinitionUploadOutcome): FlowDefinitionUploadOutcome {
  switch (outcome) {
    case ProtoFlowDefinitionUploadOutcome.FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED:
      return FlowDefinitionUploadOutcome.CREATED;
    case ProtoFlowDefinitionUploadOutcome.FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED:
      return FlowDefinitionUploadOutcome.REPLACED;
    case ProtoFlowDefinitionUploadOutcome.FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED:
      return FlowDefinitionUploadOutcome.UNCHANGED;
    case ProtoFlowDefinitionUploadOutcome.FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING:
      return FlowDefinitionUploadOutcome.KEPT_EXISTING;
    default:
      return FlowDefinitionUploadOutcome.UNSPECIFIED;
  }
}

function flowDefinitionFiles(paths: readonly string[]): string[] {
  const files: string[] = [];
  for (const configuredPath of paths) {
    let isDirectory: boolean;
    try {
      isDirectory = statSync(configuredPath).isDirectory();
    } catch (failure) {
      throw new Error(`Flow definition path ${JSON.stringify(configuredPath)} does not exist`, { cause: failure });
    }
    if (!isDirectory) {
      files.push(configuredPath);
      continue;
    }
    const entries = readdirSync(configuredPath, { recursive: true, withFileTypes: true });
    files.push(
      ...entries
        .filter((entry) => entry.isFile() && entry.name.endsWith(".json"))
        .map((entry) => path.join(entry.parentPath, entry.name))
        .sort(),
    );
  }
  return files;
}

function loadFlowDefinitionFile(filePath: string): PreparedFlowDefinition {
  const origin = `file ${JSON.stringify(filePath)}`;
  let content: Uint8Array;
  let modifiedAt: Date;
  try {
    content = readFileSync(filePath);
    modifiedAt = statSync(filePath).mtime;
  } catch (failure) {
    throw new Error(`cannot read Flow definition ${origin}`, { cause: failure });
  }
  const { text, document } = parseFlowDefinition(content, origin);
  const flowType = flowName(document, origin);
  const generatedAt = "generatedAt" in document ? parseGeneratedAt(document.generatedAt, origin) : modifiedAt;
  return { flowType, content: rewriteSourcePath(content, text, document), generatedAt, origin };
}

function prepareDocument(document: FlowDefinitionDocument): PreparedFlowDefinition {
  if (typeof document.flowType !== "string" || document.flowType === "") {
    throw new TypeError("FlowDefinitionDocument.flowType must not be empty");
  }
  if (!(document.generatedAt instanceof Date) || Number.isNaN(document.generatedAt.getTime())) {
    throw new TypeError(`FlowDefinitionDocument.generatedAt for Flow type ${JSON.stringify(document.flowType)} must be a valid Date`);
  }
  const origin = `in-memory document for Flow type ${JSON.stringify(document.flowType)}`;
  let content: Uint8Array;
  if (typeof document.content === "string") {
    content = new TextEncoder().encode(document.content);
  } else if (document.content instanceof Uint8Array) {
    content = document.content;
  } else {
    throw new TypeError(`Flow definition ${origin} content must be a string or Uint8Array`);
  }
  const { text, document: parsed } = parseFlowDefinition(content, origin);
  const name = flowName(parsed, origin);
  if (name !== document.flowType) {
    throw new Error(`Flow definition ${origin} has flow.name ${JSON.stringify(name)}; it must equal the Flow type`);
  }
  return {
    flowType: document.flowType,
    content: rewriteSourcePath(content, text, parsed),
    generatedAt: document.generatedAt,
    origin,
  };
}

function parseFlowDefinition(
  content: Uint8Array,
  origin: string,
): { readonly text: string; readonly document: Record<string, unknown> } {
  let text: string;
  let document: unknown;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(content);
    document = JSON.parse(text);
  } catch (failure) {
    throw new Error(`Flow definition ${origin} is not UTF-8 JSON`, { cause: failure });
  }
  if (!isJsonObject(document)) {
    throw new Error(`Flow definition ${origin} is not a JSON object`);
  }
  return { text, document };
}

function flowName(document: Record<string, unknown>, origin: string): string {
  const flow = document.flow;
  const name = isJsonObject(flow) ? flow.name : undefined;
  if (typeof name !== "string" || name === "") {
    throw new Error(`Flow definition ${origin} has no flow.name string`);
  }
  return name;
}

function parseGeneratedAt(value: unknown, origin: string): Date {
  if (typeof value !== "string" || !rfc3339Pattern.test(value)) {
    throw new Error(`Flow definition ${origin} has generatedAt ${JSON.stringify(value)}, which is not an RFC 3339 time`);
  }
  const generatedAt = new Date(value);
  if (Number.isNaN(generatedAt.getTime())) {
    throw new Error(`Flow definition ${origin} has generatedAt ${JSON.stringify(value)}, which is not an RFC 3339 time`);
  }
  return generatedAt;
}

function rewriteSourcePath(content: Uint8Array, text: string, document: Record<string, unknown>): Uint8Array {
  const source = document.source;
  if (!isJsonObject(source) || typeof source.path !== "string") {
    return content;
  }
  const replacements: { readonly start: number; readonly end: number; readonly encodedPath: string }[] = [];
  for (const member of jsonObjectMembers(text, skipJsonWhitespace(text, 0))) {
    if (member.key !== "source" || text[member.valueStart] !== "{") continue;
    for (const sourceMember of jsonObjectMembers(text, member.valueStart)) {
      if (sourceMember.key !== "path" || text[sourceMember.valueStart] !== "\"") continue;
      const originalPath = JSON.parse(text.slice(sourceMember.valueStart, sourceMember.valueEnd)) as string;
      const sharedPath = shareableSourcePath(originalPath);
      if (sharedPath !== originalPath) {
        replacements.push({ start: sourceMember.valueStart, end: sourceMember.valueEnd, encodedPath: JSON.stringify(sharedPath) });
      }
    }
  }
  if (replacements.length === 0) {
    return content;
  }
  let rewritten = text;
  for (const replacement of replacements.reverse()) {
    rewritten = rewritten.slice(0, replacement.start) + replacement.encodedPath + rewritten.slice(replacement.end);
  }
  return new TextEncoder().encode(rewritten);
}

function shareableSourcePath(sourcePath: string): string {
  if (path.isAbsolute(sourcePath)) {
    const relativePath = path.relative(process.cwd(), sourcePath);
    const isUnderWorkingDirectory = relativePath !== ""
      && relativePath !== ".."
      && !relativePath.startsWith(`..${path.sep}`)
      && !path.isAbsolute(relativePath);
    return isUnderWorkingDirectory ? relativePath.split(path.sep).join("/") : path.basename(sourcePath);
  }
  if (path.win32.isAbsolute(sourcePath)) {
    return path.win32.basename(sourcePath);
  }
  return sourcePath;
}

// The text was already validated by JSON.parse, so the scanner only finds value boundaries.
function* jsonObjectMembers(text: string, objectStart: number): Generator<JsonObjectMember> {
  let position = skipJsonWhitespace(text, objectStart + 1);
  if (text[position] === "}") return;
  for (;;) {
    const keyEnd = jsonStringEnd(text, position);
    const key = JSON.parse(text.slice(position, keyEnd)) as string;
    const valueStart = skipJsonWhitespace(text, skipJsonWhitespace(text, keyEnd) + 1);
    const valueEnd = jsonValueEnd(text, valueStart);
    yield { key, valueStart, valueEnd };
    position = skipJsonWhitespace(text, valueEnd);
    if (text[position] === "}") return;
    position = skipJsonWhitespace(text, position + 1);
  }
}

function jsonValueEnd(text: string, start: number): number {
  const first = text[start];
  if (first === "\"") return jsonStringEnd(text, start);
  if (first === "{" || first === "[") {
    let depth = 0;
    let position = start;
    for (;;) {
      const character = text[position];
      if (character === "\"") {
        position = jsonStringEnd(text, position);
        continue;
      }
      if (character === "{" || character === "[") {
        depth += 1;
      } else if (character === "}" || character === "]") {
        depth -= 1;
        if (depth === 0) return position + 1;
      }
      position += 1;
    }
  }
  let position = start;
  while (position < text.length && !jsonScalarTerminators.includes(text[position] as string)) {
    position += 1;
  }
  return position;
}

function jsonStringEnd(text: string, start: number): number {
  let position = start + 1;
  while (text[position] !== "\"") {
    position += text[position] === "\\" ? 2 : 1;
  }
  return position + 1;
}

function skipJsonWhitespace(text: string, start: number): number {
  let position = start;
  while (position < text.length && jsonWhitespace.includes(text[position] as string)) {
    position += 1;
  }
  return position;
}

function isJsonObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
