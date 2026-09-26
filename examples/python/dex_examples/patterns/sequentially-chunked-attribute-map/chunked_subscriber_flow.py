# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

from dataclasses import dataclass

from dex import Attribute, AttributeMap, Context, Flow, PersistenceSchema, RPCResult, StepList, rpc

FLOW_ID = "chunked-subscriber-archive"
CURRENT_CHUNK_INSTANCE = "current"
SUBSCRIBER_CHUNK_SIZE = 100


@dataclass
class Subscriber:
    sequence: int
    subscriber_id: str
    delivery_address: str


@dataclass
class SubscriberChunk:
    subscribers: list[Subscriber]


@dataclass
class SubscriberArchiveState:
    next_sequence: int
    latest_archived_chunk_token: str = ""


@dataclass
class RegisterSubscriberInput:
    subscriber_id: str
    delivery_address: str


@dataclass
class GetSubscriberPageInput:
    page_token: str


@dataclass
class SubscriberPage:
    page_token: str
    subscribers: list[Subscriber]
    next_page_token: str = ""


class ChunkedSubscriberFlow(Flow[None]):
    archive_state = Attribute("subscriber_archive_state", SubscriberArchiveState)
    subscriber_chunks = AttributeMap("subscriber_chunks", SubscriberChunk)

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.archive_state, self.subscriber_chunks)

    @rpc(
        name="RegisterSubscriber",
        lock_attributes=(archive_state.lock(), subscriber_chunks.lock(CURRENT_CHUNK_INSTANCE)),
        load_attribute_map_instances=(subscriber_chunks.load(CURRENT_CHUNK_INSTANCE),),
    )
    def register_subscriber(
        self,
        context: Context,
        input: RegisterSubscriberInput,
    ) -> RPCResult[Subscriber]:
        if not input.subscriber_id:
            raise ValueError("subscriberId is required")
        if not input.delivery_address:
            raise ValueError("deliveryAddress is required")

        archive_state = self.archive_state.get(context)
        current_chunk = self.subscriber_chunks.get(context, CURRENT_CHUNK_INSTANCE)
        subscriber = Subscriber(
            archive_state.next_sequence,
            input.subscriber_id,
            input.delivery_address,
        )
        if len(current_chunk.subscribers) == SUBSCRIBER_CHUNK_SIZE:
            archive_token = archive_chunk_token(current_chunk.subscribers[0].sequence)
            self.subscriber_chunks.set(context, archive_token, current_chunk)
            archive_state.latest_archived_chunk_token = archive_token
            current_chunk = SubscriberChunk([subscriber])
        else:
            current_chunk.subscribers.append(subscriber)
        archive_state.next_sequence += 1
        self.subscriber_chunks.set(context, CURRENT_CHUNK_INSTANCE, current_chunk)
        self.archive_state.set(context, archive_state)
        return RPCResult(subscriber)

    @rpc(name="GetSubscriberPage")
    def get_subscriber_page(
        self,
        context: Context,
        input: GetSubscriberPageInput,
    ) -> RPCResult[SubscriberPage]:
        page_token = validate_subscriber_page_token(input.page_token)
        try:
            chunk = self.subscriber_chunks.get(context, page_token)
        except KeyError as error:
            raise ValueError(f'subscriber page "{page_token}" not found') from error
        page = SubscriberPage(page_token, list(reversed(chunk.subscribers)))
        if not chunk.subscribers:
            return RPCResult(page)
        if page_token == CURRENT_CHUNK_INSTANCE:
            page.next_page_token = self.archive_state.get(
                context
            ).latest_archived_chunk_token
        else:
            first_sequence = int(page_token)
            if first_sequence > 1:
                page.next_page_token = archive_chunk_token(
                    first_sequence - SUBSCRIBER_CHUNK_SIZE
                )
        return RPCResult(page)


def validate_subscriber_page_token(page_token: str) -> str:
    if not page_token:
        return CURRENT_CHUNK_INSTANCE
    if page_token == CURRENT_CHUNK_INSTANCE:
        return page_token
    if len(page_token) != 20 or not page_token.isascii() or not page_token.isdigit():
        raise ValueError(f'invalid page token "{page_token}"')
    first_sequence = int(page_token)
    if first_sequence == 0 or (first_sequence - 1) % SUBSCRIBER_CHUNK_SIZE != 0:
        raise ValueError(f'invalid page token "{page_token}"')
    return page_token


def archive_chunk_token(first_sequence: int) -> str:
    return f"{first_sequence:020d}"
