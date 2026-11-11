// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * One in-memory Flow Definition Graph (FDG 2.0) JSON document to upload.
 *
 * Use it when the application builds the graph in memory instead of reading a file that
 * `dexcli visualize` wrote. Pass documents as `WorkerOptions.flowDefinitions` so the Worker
 * uploads them when it starts, or to `Client.uploadFlowDefinitions` from a deploy script.
 *
 * The Server keeps one document for each Flow type. It replaces a stored document only when the
 * uploaded one has a different digest and a later `generatedAt`. Give a document the same
 * `generatedAt` every time its content is the same, such as the release time of the code that
 * renders it.
 *
 * The SDK checks that `content` is a JSON object whose `schemaVersion` starts with `"2."` and whose
 * `flow.name` equals `flowType`. The Server also enforces its size and depth limits. Before
 * uploading, the SDK rewrites an absolute top-level `source.path` inside the document to a path
 * relative to the current working directory, or to the file name when the file is outside it or
 * the directory is unknown, so local paths are not shared. The comparison is lexical: "." and ".."
 * are resolved and symbolic links are not. It leaves the other bytes unchanged.
 *
 * @since 1.45.0
 * @example
 * ```ts
 * const document: FlowDefinitionDocument = {
 *   flowType: "OrderFlow",
 *   content: renderOrderFlowGraph(),
 *   generatedAt: new Date("2026-11-01T00:00:00Z"),
 * };
 * const worker = new Worker(registry, cache, { flowDefinitions: [document] });
 * ```
 */
export interface FlowDefinitionDocument {
  /** Registered Flow type that the document describes. It must not be empty and must equal `flow.name` in `content`. */
  readonly flowType: string;
  /** The FDG JSON document as a string or as UTF-8 bytes. */
  readonly content: string | Uint8Array;
  /**
   * When the document was generated.
   *
   * The Server rejects a time more than 24 hours after its own clock. Every Dex SDK truncates the
   * time to whole milliseconds, which is the precision of a JavaScript Date.
   */
  readonly generatedAt: Date;
}

/**
 * What the Server did with one uploaded Flow definition.
 *
 * @since 1.45.0
 * @example
 * ```ts
 * const [result] = await client.uploadFlowDefinitions([document]);
 * if (result?.outcome === FlowDefinitionUploadOutcome.KEPT_EXISTING) {
 *   console.warn("a newer definition is already stored");
 * }
 * ```
 */
export const FlowDefinitionUploadOutcome = Object.freeze({
  /** No document was stored for the Flow type; the uploaded one is now stored. */
  CREATED: "created",
  /** The uploaded document replaced a different, older one. */
  REPLACED: "replaced",
  /** The stored document has the same digest; nothing was written. */
  UNCHANGED: "unchanged",
  /**
   * The stored document differs and its generatedAt is the same or later, so it stays stored.
   * Upload with `force` to replace it anyway.
   */
  KEPT_EXISTING: "keptExisting",
  /**
   * The Server returned no outcome or one this SDK version does not know.
   *
   * A newer Server can return such an outcome. Check `storedDigest` and `storedGeneratedAt` for the
   * stored document.
   */
  UNSPECIFIED: "unspecified",
} as const);

/**
 * Represents a value from {@link FlowDefinitionUploadOutcome}.
 *
 * @since 1.45.0
 */
export type FlowDefinitionUploadOutcome =
  (typeof FlowDefinitionUploadOutcome)[keyof typeof FlowDefinitionUploadOutcome];

/**
 * The outcome of uploading one Flow definition and the document stored afterwards.
 *
 * `Client.uploadFlowDefinitions` returns one result for each uploaded document, in request order.
 *
 * @since 1.45.0
 * @example
 * ```ts
 * for (const result of await client.uploadFlowDefinitions(documents)) {
 *   console.log(result.flowType, result.outcome, result.storedDigest);
 * }
 * ```
 */
export interface FlowDefinitionUploadResult {
  /** Flow type of the uploaded document. */
  readonly flowType: string;
  /** What the Server did with the uploaded document. */
  readonly outcome: FlowDefinitionUploadOutcome;
  /**
   * Lowercase hexadecimal SHA-256 digest of the document stored after the call.
   *
   * It differs from the uploaded document's digest only for `KEPT_EXISTING`.
   */
  readonly storedDigest: string;
  /** generatedAt of the document stored after the call. */
  readonly storedGeneratedAt: Date;
}
