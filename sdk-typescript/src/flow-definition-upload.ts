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

const schemaVersionPrefix = "2.";
const rfc3339Pattern = /^(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(?:([Zz])|([+-])(\d{2}):(\d{2}))$/;
const drivePattern = /^[A-Za-z]:/;
const absoluteDrivePattern = /^[A-Za-z]:[\\/]/;
const pathSeparatorPattern = /[\\/]/;
const jsonWhitespace = " \t\n\r";
const jsonScalarTerminators = ",}] \t\n\r";
const jsonShortEscapes: Readonly<Record<string, string>> = {
  "\"": "\\\"",
  "\\": "\\\\",
  "\b": "\\b",
  "\f": "\\f",
  "\n": "\\n",
  "\r": "\\r",
  "\t": "\\t",
};

export interface PreparedFlowDefinition {
  readonly flowType: string;
  readonly content: Uint8Array;
  readonly generatedAt: Date;
  readonly origin: string;
}

export type FlowDefinitionUploadCall = (
  request: UploadFlowDefinitionsRequest,
  deadline: Date,
) => Promise<UploadFlowDefinitionsResponse>;

interface JsonObjectMember {
  readonly key: string;
  readonly valueStart: number;
  readonly valueEnd: number;
}

/** A file that is not a Flow Definition Graph 2.x document. */
export class InvalidFlowDefinitionError extends Error {}

/**
 * Loads, validates, and merges the configured Flow definitions.
 * @returns One definition per Flow type: files first, then in-memory documents in the given order.
 * @throws {Error} A path is missing or unreadable, an explicit file or document is invalid, a
 * directory FDG has an invalid generatedAt, or a Flow type repeats.
 */
export function prepareFlowDefinitions(
  paths: readonly string[],
  documents: readonly FlowDefinitionDocument[],
): PreparedFlowDefinition[] {
  const workingDirectory = currentWorkingDirectory();
  const prepared = [
    ...paths.flatMap((configuredPath) => loadConfiguredPath(configuredPath, workingDirectory)),
    ...documents.map((document) => prepareDocument(document, workingDirectory)),
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
  return flowDefinitionBatches(definitions).map((batch) => ({
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
  }));
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

/** Uploads a Worker's Flow definitions within one total deadline, logging every failure instead of throwing. */
export async function uploadWorkerFlowDefinitions(
  upload: FlowDefinitionUploadCall,
  definitions: readonly PreparedFlowDefinition[],
  registeredFlowTypes: ReadonlySet<string>,
  uploader: string,
  timeoutMs: number,
): Promise<void> {
  const selected = definitions.filter((definition) => {
    if (registeredFlowTypes.has(definition.flowType)) return true;
    logInfo(
      `skipping Flow definition upload because this Worker does not register the Flow type: flowType=${definition.flowType} origin=${definition.origin}`,
    );
    return false;
  });
  const deadline = new Date(Date.now() + timeoutMs);
  for (const { request, batch } of uploadRequests(selected, uploader, false)) {
    if (Date.now() >= deadline.getTime()) {
      console.warn("Flow definition upload ran out of time before every batch was sent; the Worker continues starting");
      return;
    }
    try {
      const response = await upload(request, deadline);
      logWorkerUploadResults(batch, response);
    } catch (failure) {
      logWorkerUploadFailure(failure);
      return;
    }
  }
}

/**
 * Returns the shared form of an absolute `source.path`.
 * @returns `undefined` when the path is not absolute and stays unchanged; otherwise the path relative
 * to `workingDirectory` joined with "/", or the base name when it is outside or the directory is unknown.
 */
export function shareableSourcePath(sourcePath: string, workingDirectory: string | undefined): string | undefined {
  if (!isAbsoluteSourcePath(sourcePath)) {
    return undefined;
  }
  const pathSegments = lexicalPathSegments(sourcePath);
  if (workingDirectory !== undefined) {
    const directorySegments = lexicalPathSegments(workingDirectory);
    const isUnderDirectory = pathSegments.length > directorySegments.length
      && directorySegments.every((segment, index) => pathSegments[index] === segment);
    if (isUnderDirectory) {
      return pathSegments.slice(directorySegments.length).join("/");
    }
  }
  return pathSegments[pathSegments.length - 1] as string;
}

/**
 * Encodes a JSON string escaping only quotes, backslashes, and control characters.
 * @returns The quoted JSON string.
 */
export function encodeJsonString(value: string): string {
  let encoded = "\"";
  for (const character of value) {
    const escape = jsonShortEscapes[character];
    if (escape !== undefined) {
      encoded += escape;
    } else if (character.charCodeAt(0) < 0x20) {
      encoded += `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
    } else {
      encoded += character;
    }
  }
  return `${encoded}"`;
}

/**
 * Parses a strict RFC 3339 time truncated to whole milliseconds.
 * @returns The parsed time.
 * @throws {Error} The value is not a strict RFC 3339 date and time with an offset.
 */
export function parseRfc3339Milliseconds(value: string): Date {
  const match = rfc3339Pattern.exec(value);
  if (match === null) {
    throw new Error(`${JSON.stringify(value)} is not an RFC 3339 time`);
  }
  const [, year, month, day, hour, minute, second, fraction, , sign, offsetHour, offsetMinute] = match;
  const fields = [year, month, day, hour, minute, second].map(Number) as [number, number, number, number, number, number];
  const milliseconds = Number((fraction ?? "").padEnd(3, "0").slice(0, 3));
  const local = new Date(0);
  local.setUTCFullYear(fields[0], fields[1] - 1, fields[2]);
  local.setUTCHours(fields[3], fields[4], fields[5], milliseconds);
  const localMillis = local.getTime();
  const isValidCalendarTime = local.getUTCFullYear() === fields[0]
    && local.getUTCMonth() === fields[1] - 1
    && local.getUTCDate() === fields[2]
    && local.getUTCHours() === fields[3]
    && local.getUTCMinutes() === fields[4]
    && local.getUTCSeconds() === fields[5];
  const offsetMinutes = sign === undefined ? 0 : (Number(offsetHour) * 60 + Number(offsetMinute)) * (sign === "-" ? -1 : 1);
  const isValidOffset = sign === undefined || (Number(offsetHour) <= 23 && Number(offsetMinute) <= 59);
  if (!isValidCalendarTime || !isValidOffset) {
    throw new Error(`${JSON.stringify(value)} is not an RFC 3339 time`);
  }
  return new Date(localMillis - offsetMinutes * 60_000);
}

function logInfo(message: string): void {
  process.stderr.write(`dex info: ${message}\n`);
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
  const results = uploadResultsFromResponse(response);
  results.forEach((result, index) => {
    const definition = definitionsByFlowType.get(result.flowType);
    const origin = definition?.origin ?? "unknown";
    // Node has no log levels, so an unchanged definition stays silent on every restart.
    if (result.outcome === FlowDefinitionUploadOutcome.UNCHANGED) {
      return;
    }
    if (result.outcome === FlowDefinitionUploadOutcome.UNSPECIFIED) {
      console.warn(
        `unknown upload outcome ${String(response.results[index]?.outcome)}: flowType=${result.flowType} origin=${origin} storedDigest=${result.storedDigest}`,
      );
    } else if (result.outcome === FlowDefinitionUploadOutcome.KEPT_EXISTING) {
      const uploadedDigest = definition === undefined
        ? "unknown"
        : createHash("sha256").update(definition.content).digest("hex");
      console.warn(
        "Dex Server kept a different Flow definition generated at the same time or later; upload with force to replace it: "
          + `flowType=${result.flowType} origin=${origin} uploadedDigest=${uploadedDigest} `
          + `uploadedGeneratedAt=${definition?.generatedAt.toISOString() ?? "unknown"} `
          + `storedDigest=${result.storedDigest} storedGeneratedAt=${result.storedGeneratedAt.toISOString()}`,
      );
    } else {
      logInfo(`Flow definition ${result.outcome}: flowType=${result.flowType} origin=${origin} digest=${result.storedDigest}`);
    }
  });
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

function loadConfiguredPath(configuredPath: string, workingDirectory: string | undefined): PreparedFlowDefinition[] {
  let isDirectory: boolean;
  try {
    isDirectory = statSync(configuredPath).isDirectory();
  } catch (failure) {
    throw new Error(`Flow definition path ${JSON.stringify(configuredPath)} does not exist`, { cause: failure });
  }
  if (!isDirectory) {
    return [loadFlowDefinitionFile(configuredPath, workingDirectory)];
  }
  let entries;
  try {
    entries = readdirSync(configuredPath, { recursive: true, withFileTypes: true });
  } catch (failure) {
    throw new Error(`cannot read Flow definition directory ${JSON.stringify(configuredPath)}`, { cause: failure });
  }
  const candidates = entries
    .filter((entry) => entry.isFile() && path.extname(entry.name).toLowerCase() === ".json")
    .map((entry) => path.join(entry.parentPath, entry.name))
    .sort();
  const loaded: PreparedFlowDefinition[] = [];
  for (const candidate of candidates) {
    try {
      loaded.push(loadFlowDefinitionFile(candidate, workingDirectory));
    } catch (failure) {
      if (!(failure instanceof InvalidFlowDefinitionError)) throw failure;
      logInfo(`skipping a file that is not a Flow definition: ${failure.message}`);
    }
  }
  return loaded;
}

function loadFlowDefinitionFile(filePath: string, workingDirectory: string | undefined): PreparedFlowDefinition {
  const origin = `file ${JSON.stringify(filePath)}`;
  let content: Uint8Array;
  let modifiedAt: Date;
  try {
    content = readFileSync(filePath);
    modifiedAt = new Date(Math.floor(statSync(filePath).mtimeMs));
  } catch (failure) {
    throw new Error(`cannot read Flow definition ${origin}`, { cause: failure });
  }
  const { flowType, generatedAt } = parseFlowDefinitionContent(content, origin);
  return { flowType, content: rewriteSourcePath(content, workingDirectory), generatedAt: generatedAt ?? modifiedAt, origin };
}

function prepareDocument(document: FlowDefinitionDocument, workingDirectory: string | undefined): PreparedFlowDefinition {
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
  // The content's generatedAt must be valid even though the caller's time is used.
  const { flowType: name } = parseFlowDefinitionContent(content, origin);
  if (name !== document.flowType) {
    throw new Error(`Flow definition ${origin} has flow.name ${JSON.stringify(name)}; it must equal the Flow type`);
  }
  return {
    flowType: document.flowType,
    content: rewriteSourcePath(content, workingDirectory),
    generatedAt: new Date(Math.floor(document.generatedAt.getTime())),
    origin,
  };
}

/**
 * Validates a Flow Definition Graph 2.x document.
 * @returns The Flow type and the generatedAt time truncated to milliseconds, or `undefined` without one.
 * @throws {InvalidFlowDefinitionError} The content is not a strict JSON FDG 2.x object with a flow.name.
 * @throws {Error} The document is an FDG 2.x document with an invalid generatedAt.
 */
export function parseFlowDefinitionContent(
  content: Uint8Array,
  origin: string,
): { readonly flowType: string; readonly generatedAt: Date | undefined } {
  const { document } = parseFlowDefinition(content, origin);
  const flowType = flowName(document, origin);
  const generatedAt = "generatedAt" in document ? parseGeneratedAt(document.generatedAt, origin) : undefined;
  return { flowType, generatedAt };
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
    throw new InvalidFlowDefinitionError(`Flow definition ${origin} is not UTF-8 JSON`, { cause: failure });
  }
  if (!isJsonObject(document)) {
    throw new InvalidFlowDefinitionError(`Flow definition ${origin} is not a JSON object`);
  }
  const schemaVersion = document.schemaVersion;
  if (typeof schemaVersion !== "string" || !schemaVersion.startsWith(schemaVersionPrefix)) {
    throw new InvalidFlowDefinitionError(
      `Flow definition ${origin} has schemaVersion ${JSON.stringify(schemaVersion)}; it must start with "2."`,
    );
  }
  return { text, document };
}

function flowName(document: Record<string, unknown>, origin: string): string {
  const flow = document.flow;
  const name = isJsonObject(flow) ? flow.name : undefined;
  if (typeof name !== "string" || name === "") {
    throw new InvalidFlowDefinitionError(`Flow definition ${origin} has no flow.name string`);
  }
  return name;
}

function parseGeneratedAt(value: unknown, origin: string): Date {
  try {
    if (typeof value !== "string") throw new Error("generatedAt is not a string");
    return parseRfc3339Milliseconds(value);
  } catch (failure) {
    throw new Error(`Flow definition ${origin} has generatedAt ${JSON.stringify(value)}, which is not RFC 3339`, { cause: failure });
  }
}

/**
 * Rewrites an absolute top-level `source.path` in validated FDG content.
 * @returns `content` itself unless the top-level object has exactly one `source` key whose object value
 * has exactly one string `path` key that is absolute; then the content with only that string replaced.
 */
export function rewriteSourcePath(content: Uint8Array, workingDirectory: string | undefined): Uint8Array {
  const text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(content);
  const sources = [...jsonObjectMembers(text, skipJsonWhitespace(text, 0))].filter((member) => member.key === "source");
  const [source] = sources;
  if (sources.length !== 1 || source === undefined || text[source.valueStart] !== "{") {
    return content;
  }
  const paths = [...jsonObjectMembers(text, source.valueStart)].filter((member) => member.key === "path");
  const [sourcePath] = paths;
  if (paths.length !== 1 || sourcePath === undefined || text[sourcePath.valueStart] !== "\"") {
    return content;
  }
  const originalPath = JSON.parse(text.slice(sourcePath.valueStart, sourcePath.valueEnd)) as string;
  const sharedPath = shareableSourcePath(originalPath, workingDirectory);
  if (sharedPath === undefined) {
    return content;
  }
  return new TextEncoder().encode(
    text.slice(0, sourcePath.valueStart) + encodeJsonString(sharedPath) + text.slice(sourcePath.valueEnd),
  );
}

function currentWorkingDirectory(): string | undefined {
  try {
    return process.cwd();
  } catch {
    // A deleted working directory makes cwd fail; the rewrite then keeps base names.
    return undefined;
  }
}

function isAbsoluteSourcePath(sourcePath: string): boolean {
  return sourcePath.startsWith("/") || sourcePath.startsWith("\\\\") || absoluteDrivePattern.test(sourcePath);
}

// Returns the root ("/", "//" for UNC, an uppercase drive, or "") then segments with "." and ".." resolved.
function lexicalPathSegments(sourcePath: string): string[] {
  let root: string;
  let remainder: string;
  if (drivePattern.test(sourcePath)) {
    root = sourcePath.slice(0, 2).toUpperCase();
    remainder = sourcePath.slice(2);
  } else if (sourcePath.startsWith("\\\\")) {
    root = "//";
    remainder = sourcePath.slice(2);
  } else if (sourcePath.startsWith("/")) {
    root = "/";
    remainder = sourcePath;
  } else {
    root = "";
    remainder = sourcePath;
  }
  const segments: string[] = [];
  for (const segment of remainder.split(pathSeparatorPattern)) {
    if (segment === "" || segment === ".") continue;
    if (segment === "..") {
      segments.pop();
      continue;
    }
    segments.push(segment);
  }
  return [root, ...segments];
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
