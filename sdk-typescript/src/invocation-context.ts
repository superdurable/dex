// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
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
import type { RegisteredFlow } from "./flow.js";
import { AttributeMap, IndexType, type Attribute } from "./persistence.js";
import { decodeValue, deletionValue, encodeValue } from "./value-mapper.js";
import { requireName } from "./validation.js";
import { ChannelMap, type Channel } from "./wait.js";

export type InvocationMethod = "waitFor" | "execute" | "rpc";

export class InvocationContext implements Context {
  public readonly flowId: string;
  public readonly runId: string;
  public readonly flowStartedAt: Date;
  public readonly stepExecutionId: string;
  public readonly fromStepExecutionId: string;
  public readonly firstAttemptAt: Date;
  public readonly attempt: number;

  private readonly attributes: Map<string, Value>;
  private readonly locals: Map<string, Value>;
  private readonly channelInfos: Map<string, ChannelInfo>;
  private readonly attributeWrites = new Map<string, AttributeWrite>();
  private readonly localWrites = new Map<string, KV>();
  private readonly events: KV[] = [];
  private readonly eventNames = new Set<string>();
  private readonly publications: ChannelMessage[] = [];

  public constructor(
    private readonly method: InvocationMethod,
    private readonly flow: RegisteredFlow,
    metadata: ProtoContext | undefined,
    attributes: readonly KV[],
    locals: readonly KV[] = [],
    private readonly conditionResults?: ConditionResults,
    channelInfos: Readonly<Record<string, ChannelInfo>> = {},
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
    this.attributes = mapValues("Attribute", attributes);
    this.locals = mapValues("step-execution local", locals);
    this.channelInfos = new Map(Object.entries(channelInfos));
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
