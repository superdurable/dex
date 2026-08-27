// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import { FlowServiceClient, type WriteStreamRequest } from "./gen/dex.js";
import { mapAttributeStoreSync } from "./attribute-store-sync.js";
import type { Context } from "./context.js";
import {
  ConditionStatus,
  IndexType as ProtoIndexType,
  type AttributeWrite,
  type ChannelInfo,
  type ChannelMessage,
  type ConditionResults,
  type Context as ProtoContext,
  type IndexConfig,
  type KV,
  type Value,
} from "./gen/dex.js";
import { requireFlowStream, type RegisteredFlow } from "./flow.js";
import { createFlowResultFromProto, type FlowResult } from "./flow-result.js";
import { AttributeMap, IndexType, type Attribute } from "./persistence.js";
import { decodeValue, deletionValue, encodeValue } from "./value-mapper.js";
import { requireName } from "./validation.js";
import { ChannelMap, type Channel } from "./wait.js";
import type { Stream } from "./stream.js";

export type InvocationMethod = "waitFor" | "execute" | "rpc";

export class InvocationContext implements Context {
  public readonly flowId: string;
  public readonly runId: string;
  public readonly flowStartedAt: Date;
  public readonly stepExecutionId: string;
  public readonly fromStepExecutionId: string;
  public readonly firstAttemptAt: Date;
  public readonly attempt: number;
  public readonly cancellationSignal: AbortSignal;

  private readonly attributes: Map<string, Value>;
  private readonly locals: Map<string, Value>;
  private readonly channelInfos: Map<string, ChannelInfo>;
  private readonly attributeWrites = new Map<string, AttributeWrite>();
  private readonly localWrites = new Map<string, KV>();
  private readonly events: KV[] = [];
  private readonly eventNames = new Set<string>();
  private readonly publications: ChannelMessage[] = [];
  private readonly streamWrites = new Set<Stream<unknown>>();

  public constructor(
    private readonly method: InvocationMethod,
    private readonly flow: RegisteredFlow,
    private readonly flowService: InstanceType<typeof FlowServiceClient>,
    metadata: ProtoContext | undefined,
    attributes: readonly KV[],
    locals: readonly KV[] = [],
    private readonly conditionResults?: ConditionResults,
    channelInfos: Readonly<Record<string, ChannelInfo>> = {},
    cancellationSignal: AbortSignal = new AbortController().signal,
  ) {
    if (metadata === undefined) {
      throw new TypeError("Worker request Context is required");
    }
    this.flowId = metadata.flowId;
    this.runId = metadata.runId;
    this.flowStartedAt = secondsDate(metadata.flowStartedTimestamp);
    this.stepExecutionId = metadata.stepExecutionId;
    this.fromStepExecutionId = metadata.fromStepExecutionId;
    this.firstAttemptAt = secondsDate(metadata.firstAttemptTimestamp);
    this.attempt = metadata.attempt;
    this.cancellationSignal = cancellationSignal;
    this.attributes = mapValues("Attribute", attributes);
    this.locals = mapValues("step-execution local", locals);
    this.channelInfos = new Map(Object.entries(channelInfos));
  }

  public async writeStream<T>(stream: Stream<T>, value: T): Promise<void> {
    if (this.method === "rpc") {
      throw new TypeError("Stream writes require a Step Context");
    }
    requireFlowStream(this.flow, stream as Stream<unknown>);
    if (this.streamWrites.has(stream as Stream<unknown>)) {
      throw new TypeError(`Stream ${stream.name} was already written by this Step execution`);
    }
    this.streamWrites.add(stream as Stream<unknown>);
    try {
      await writeStream(this.flowService, {
        flowId: this.flowId,
        flowType: this.flow.name,
        streamName: stream.name,
        maxEstimatedBytes: BigInt(stream.maxEstimatedBytes),
        value: encodeValue(stream.codec, value),
        idempotencyKey: `${this.runId}#${this.stepExecutionId}`,
      });
    } catch (failure) {
      this.streamWrites.delete(stream as Stream<unknown>);
      throw failure;
    }
  }

  public hasTimerFired(index?: number): boolean {
    const results = this.conditionResults?.timerResults ?? [];
    if (index !== undefined) {
      return results[index]?.conditionStatus === ConditionStatus.CONDITION_STATUS_COMPLETED;
    }
    return results.some(
      (result) => result.conditionStatus === ConditionStatus.CONDITION_STATUS_COMPLETED,
    );
  }

  public waitForMethodFailed(): boolean {
    return this.conditionResults?.waitForFailed ?? false;
  }

  public subFlowResult(index = 0): FlowResult {
    if (this.method !== "execute") {
      throw new TypeError("SubFlow results are available only during execute");
    }
    const result = this.conditionResults?.subFlowResults[index];
    if (index < 0 || result === undefined) {
      throw new RangeError(`SubFlow result index is unavailable: ${index}`);
    }
    return createFlowResultFromProto(
      result,
      result.results.map((completion) => {
        if (completion.completedStepOutput === undefined) {
          throw new TypeError("SubFlow Step completion output is required");
        }
        return completion.completedStepOutput;
      }),
    );
  }

  public subFlowId(index = 0): string {
    this.subFlowResult(index);
    return `SubFlow:${this.flowId}-${this.stepExecutionId}-${index}`;
  }

  public setStepExecutionLocal<T>(key: string, value: T, codec: Codec<T>): void {
    requireName(key);
    this.localWrites.set(key, { key, value: encodeValue(codec, value) });
  }

  public getStepExecutionLocal<T>(key: string, codec: Codec<T>): T | undefined {
    requireName(key);
    const value = this.localWrites.get(key)?.value ?? this.locals.get(key);
    return value === undefined ? undefined : decodeValue(codec, value);
  }

  public recordEvent<T>(name: string, value: T, codec: Codec<T>): void {
    requireName(name);
    if (this.eventNames.has(name)) {
      throw new TypeError(`event was already recorded: ${name}`);
    }
    this.eventNames.add(name);
    this.events.push({ key: name, value: encodeValue(codec, value) });
  }

  public getAttribute<T>(
    attribute: Attribute<T> | AttributeMap<T>,
    instance?: string,
  ): T {
    this.requireRegistered(attribute);
    const key = definitionName(attribute, instance);
    const write = this.attributeWrites.get(key)?.value;
    if (write?.kind?.$case === "nullValue") {
      return defaultValue(attribute.codec);
    }
    const value = write ?? this.attributes.get(key);
    return value === undefined ? defaultValue(attribute.codec) : decodeValue(attribute.codec, value);
  }

  public setAttribute<T>(
    attribute: Attribute<T> | AttributeMap<T>,
    value: T,
    instance?: string,
  ): void {
    this.requireRegistered(attribute);
    const key = definitionName(attribute, instance);
    this.attributeWrites.set(key, {
      key,
      value: encodeValue(attribute.codec, value),
      indexConfig: mapIndex(attribute.index),
      syncConfig: mapAttributeStoreSync(attribute),
    });
  }

  public deleteAttribute(
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instance?: string,
  ): void {
    this.requireRegistered(attribute);
    const key = definitionName(attribute, instance);
    this.attributeWrites.set(key, {
      key,
      value: deletionValue(),
      indexConfig: mapIndex(attribute.index),
      syncConfig: mapAttributeStoreSync(attribute),
    });
  }

  public attributeMapKeys(attribute: AttributeMap<unknown>): readonly string[] {
    this.requireRegistered(attribute);
    const prefix = `${attribute.name}/`;
    const physicalKeys = new Set(
      [...this.attributes.keys()].filter((key) => key.startsWith(prefix)),
    );
    for (const [key, write] of this.attributeWrites) {
      if (!key.startsWith(prefix)) {
        continue;
      }
      if (write.value?.kind?.$case === "nullValue") {
        physicalKeys.delete(key);
      } else {
        physicalKeys.add(key);
      }
    }
    return [...physicalKeys]
      .map((key) => decodeURIComponent(key.slice(prefix.length)))
      .sort();
  }

  public publish<T>(channel: Channel<T> | ChannelMap<T>, value: T, instance?: string): void {
    this.requireRegistered(channel);
    const name = definitionName(channel, instance);
    this.publications.push({ channelName: name, value: encodeValue(channel.codec, value) });
    if (this.method === "rpc") {
      this.channelInfos.set(name, { size: (this.channelInfos.get(name)?.size ?? 0) + 1 });
    }
  }

  public channelSize(
    channel: Channel<unknown> | ChannelMap<unknown>,
    instance?: string,
  ): number {
    this.requireRegistered(channel);
    return this.channelInfos.get(definitionName(channel, instance))?.size ?? 0;
  }

  public channelMapKeys(channel: ChannelMap<unknown>): readonly string[] {
    this.requireRegistered(channel);
    if (this.method !== "rpc") {
      throw new TypeError("ChannelMap introspection requires an RPC invocation");
    }
    const prefix = `${channel.name}/`;
    return [...this.channelInfos]
      .filter(([key, info]) => key.startsWith(prefix) && info.size > 0)
      .map(([key]) => decodeURIComponent(key.slice(prefix.length)))
      .sort();
  }

  public channelResults<T>(
    channel: Channel<T> | ChannelMap<T>,
    instance?: string,
  ): readonly T[] {
    this.requireRegistered(channel);
    const name = definitionName(channel, instance);
    return (this.conditionResults?.channelResults ?? [])
      .filter(
        (result) =>
          result.channelName === name &&
          result.conditionStatus === ConditionStatus.CONDITION_STATUS_COMPLETED,
      )
      .flatMap((result) => result.values.map((value) => decodeValue(channel.codec, value)));
  }

  public getAttributeWrites(): readonly AttributeWrite[] {
    return [...this.attributeWrites.values()];
  }

  public getLocalWrites(): readonly KV[] {
    return [...this.localWrites.values()];
  }

  public getEvents(): readonly KV[] {
    return this.events;
  }

  public getPublications(): readonly ChannelMessage[] {
    return this.publications;
  }

  private requireRegistered(
    definition:
      | Attribute<unknown>
      | AttributeMap<unknown>
      | Channel<unknown>
      | ChannelMap<unknown>,
  ): void {
    if (this.flow.persistence.get(definition.name) !== definition) {
      throw new TypeError(`persistence definition does not belong to Flow: ${definition.name}`);
    }
  }
}

function writeStream(
  service: InstanceType<typeof FlowServiceClient>,
  request: WriteStreamRequest,
): Promise<void> {
  return new Promise((resolve, reject) => {
    service.writeStream(request, (error) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}

function definitionName(
  definition:
    | Attribute<unknown>
    | AttributeMap<unknown>
    | Channel<unknown>
    | ChannelMap<unknown>,
  instance?: string,
): string {
  if (definition instanceof AttributeMap || definition instanceof ChannelMap) {
    if (instance === undefined) {
      throw new TypeError(`map definition ${definition.name} requires an instance`);
    }
    requireName(instance);
    return `${definition.name}/${encodeURIComponent(instance).replace(/[!'()*]/g, encodeCharacter)}`;
  }
  if (instance !== undefined) {
    throw new TypeError(`static definition ${definition.name} cannot use an instance`);
  }
  return definition.name;
}

function encodeCharacter(character: string): string {
  return `%${character.charCodeAt(0).toString(16).toUpperCase()}`;
}

function mapValues(kind: string, entries: readonly KV[]): Map<string, Value> {
  const values = new Map<string, Value>();
  for (const entry of entries) {
    if (entry.key === "" || entry.value === undefined || values.has(entry.key)) {
      throw new TypeError(`invalid or duplicate ${kind}`);
    }
    values.set(entry.key, entry.value);
  }
  return values;
}

function defaultValue<T>(codec: Codec<T>): T {
  switch (codec.wireKind) {
    case "bool":
      return false as T;
    case "double":
      return 0 as T;
    case "int64":
      return 0n as T;
    default:
      return undefined as T;
  }
}

function mapIndex(index: Attribute<unknown>["index"] | undefined): IndexConfig | undefined {
  if (index === undefined) {
    return undefined;
  }
  const types: Record<IndexType, ProtoIndexType> = {
    [IndexType.KEYWORD]: ProtoIndexType.INDEX_TYPE_KEYWORD,
    [IndexType.FULL_TEXT]: ProtoIndexType.INDEX_TYPE_TEXT,
    [IndexType.KEYWORD_ARRAY]: ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
    [IndexType.INT]: ProtoIndexType.INDEX_TYPE_INT,
    [IndexType.DOUBLE]: ProtoIndexType.INDEX_TYPE_DOUBLE,
    [IndexType.BOOL]: ProtoIndexType.INDEX_TYPE_BOOL,
    [IndexType.DATETIME]: ProtoIndexType.INDEX_TYPE_DATETIME,
  };
  return { enable: true, type: types[index.type], indexKey: index.indexKey ?? "" };
}

function secondsDate(seconds: bigint): Date {
  const milliseconds = Number(seconds * 1_000n);
  if (!Number.isSafeInteger(milliseconds)) {
    throw new RangeError("Context timestamp exceeds JavaScript's safe Date range");
  }
  return new Date(milliseconds);
}
