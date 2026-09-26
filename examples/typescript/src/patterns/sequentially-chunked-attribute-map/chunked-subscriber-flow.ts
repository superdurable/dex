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

import {
  Attribute,
  AttributeMap,
  StepList,
  jsonCodec,
  rpc,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "@superdurable/dex";

export const CHUNKED_SUBSCRIBER_FLOW_ID = "chunked-subscriber-archive";
export const CURRENT_CHUNK_INSTANCE = "current";
export const SUBSCRIBER_CHUNK_SIZE = 100;

export interface Subscriber {
  readonly sequence: number;
  readonly subscriberId: string;
  readonly deliveryAddress: string;
}

export interface SubscriberChunk {
  readonly subscribers: readonly Subscriber[];
}

export interface SubscriberArchiveState {
  readonly nextSequence: number;
  readonly latestArchivedChunkToken: string;
}

export interface RegisterSubscriberInput {
  readonly subscriberId: string;
  readonly deliveryAddress: string;
}

export interface GetSubscriberPageInput {
  readonly pageToken: string;
}

export interface SubscriberPage {
  readonly pageToken: string;
  readonly subscribers: readonly Subscriber[];
  readonly nextPageToken: string;
}

const subscriberCodec = jsonCodec<Subscriber>();
const registerSubscriberInputCodec = jsonCodec<RegisterSubscriberInput>();
const subscriberPageInputCodec = jsonCodec<GetSubscriberPageInput>();
const subscriberPageCodec = jsonCodec<SubscriberPage>();
const archiveState = new Attribute(
  "subscriber_archive_state",
  jsonCodec<SubscriberArchiveState>(),
);
const subscriberChunks = new AttributeMap(
  "subscriber_chunks",
  jsonCodec<SubscriberChunk>(),
);

export class ChunkedSubscriberFlow implements Flow<void> {
  public readonly archiveState = archiveState;
  public readonly subscriberChunks = subscriberChunks;

  public getFlowType(): string {
    return "ChunkedSubscriberFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.archiveState, this.subscriberChunks] };
  }

  @rpc({
    name: "RegisterSubscriber",
    inputCodec: registerSubscriberInputCodec,
    outputCodec: subscriberCodec,
    lockAttributes: [archiveState.lock(), subscriberChunks.lock(CURRENT_CHUNK_INSTANCE)],
    loadAttributeMapInstances: [subscriberChunks.load(CURRENT_CHUNK_INSTANCE)],
  })
  public registerSubscriber(
    context: Context,
    input: RegisterSubscriberInput,
  ): RPCResult<Subscriber> {
    if (input.subscriberId.length === 0) {
      throw new Error("subscriberId is required");
    }
    if (input.deliveryAddress.length === 0) {
      throw new Error("deliveryAddress is required");
    }
    const state = this.archiveState.get(context);
    const currentChunk = this.subscriberChunks.get(context, CURRENT_CHUNK_INSTANCE);
    const subscriber: Subscriber = {
      sequence: state.nextSequence,
      subscriberId: input.subscriberId,
      deliveryAddress: input.deliveryAddress,
    };
    let nextChunk: SubscriberChunk;
    let latestArchivedChunkToken = state.latestArchivedChunkToken;
    if (currentChunk.subscribers.length === SUBSCRIBER_CHUNK_SIZE) {
      latestArchivedChunkToken = archiveChunkToken(currentChunk.subscribers[0]!.sequence);
      this.subscriberChunks.set(context, latestArchivedChunkToken, currentChunk);
      nextChunk = { subscribers: [subscriber] };
    } else {
      nextChunk = { subscribers: [...currentChunk.subscribers, subscriber] };
    }
    this.subscriberChunks.set(context, CURRENT_CHUNK_INSTANCE, nextChunk);
    this.archiveState.set(context, {
      nextSequence: state.nextSequence + 1,
      latestArchivedChunkToken,
    });
    return { output: subscriber };
  }

  @rpc({
    name: "GetSubscriberPage",
    inputCodec: subscriberPageInputCodec,
    outputCodec: subscriberPageCodec,
  })
  public getSubscriberPage(
    context: Context,
    input: GetSubscriberPageInput,
  ): RPCResult<SubscriberPage> {
    const pageToken = validateSubscriberPageToken(input.pageToken);
    const chunk = this.subscriberChunks.get(context, pageToken);
    if (chunk === undefined) {
      throw new Error(`subscriber page "${pageToken}" not found`);
    }
    let nextPageToken = "";
    if (chunk.subscribers.length > 0) {
      if (pageToken === CURRENT_CHUNK_INSTANCE) {
        nextPageToken = this.archiveState.get(context).latestArchivedChunkToken;
      } else {
        const firstSequence = Number(pageToken);
        if (firstSequence > 1) {
          nextPageToken = archiveChunkToken(firstSequence - SUBSCRIBER_CHUNK_SIZE);
        }
      }
    }
    return {
      output: {
        pageToken,
        subscribers: [...chunk.subscribers].reverse(),
        nextPageToken,
      },
    };
  }
}

export function validateSubscriberPageToken(pageToken: string): string {
  if (pageToken.length === 0) {
    return CURRENT_CHUNK_INSTANCE;
  }
  if (pageToken === CURRENT_CHUNK_INSTANCE) {
    return pageToken;
  }
  if (!/^\d{20}$/.test(pageToken)) {
    throw new Error(`invalid page token "${pageToken}"`);
  }
  const firstSequence = Number(pageToken);
  if (firstSequence === 0 || (firstSequence - 1) % SUBSCRIBER_CHUNK_SIZE !== 0) {
    throw new Error(`invalid page token "${pageToken}"`);
  }
  return pageToken;
}

export function archiveChunkToken(firstSequence: number): string {
  return String(firstSequence).padStart(20, "0");
}

export const chunkedSubscriberFlow = new ChunkedSubscriberFlow();
