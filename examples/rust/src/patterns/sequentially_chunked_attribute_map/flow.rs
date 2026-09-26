// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeMap, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, StepList,
};
use serde::{Deserialize, Serialize};

pub const FLOW_ID: &str = "chunked-subscriber-archive";
pub const CURRENT_CHUNK_INSTANCE: &str = "current";
pub const SUBSCRIBER_CHUNK_SIZE: usize = 100;

pub const REGISTER_SUBSCRIBER: Rpc<RegisterSubscriberInput, Subscriber> =
    Rpc::new("RegisterSubscriber");
pub const GET_SUBSCRIBER_PAGE: Rpc<GetSubscriberPageInput, SubscriberPage> =
    Rpc::new("GetSubscriberPage");

pub static ARCHIVE_STATE: LazyLock<Attribute<SubscriberArchiveState>> =
    LazyLock::new(|| Attribute::new("subscriber_archive_state"));
pub static SUBSCRIBER_CHUNKS: LazyLock<AttributeMap<SubscriberChunk>> =
    LazyLock::new(|| AttributeMap::new("subscriber_chunks"));

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Subscriber {
    pub sequence: u64,
    pub subscriber_id: String,
    pub delivery_address: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SubscriberChunk {
    pub subscribers: Vec<Subscriber>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SubscriberArchiveState {
    pub next_sequence: u64,
    pub latest_archived_chunk_token: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RegisterSubscriberInput {
    pub subscriber_id: String,
    pub delivery_address: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct GetSubscriberPageInput {
    pub page_token: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SubscriberPage {
    pub page_token: String,
    pub subscribers: Vec<Subscriber>,
    pub next_page_token: String,
}

#[derive(Default)]
pub struct ChunkedSubscriberFlow;

impl ChunkedSubscriberFlow {
    fn register_subscriber(
        &self,
        context: &mut Context,
        input: RegisterSubscriberInput,
    ) -> HandlerResult<RpcResult<Subscriber>> {
        if input.subscriber_id.is_empty() {
            return Err(HandlerError::new(
                "ChunkedSubscriberFlow",
                "subscriberId is required",
            ));
        }
        if input.delivery_address.is_empty() {
            return Err(HandlerError::new(
                "ChunkedSubscriberFlow",
                "deliveryAddress is required",
            ));
        }
        let mut state = ARCHIVE_STATE.get_required(context)?;
        let mut current_chunk = SUBSCRIBER_CHUNKS.get_required(context, CURRENT_CHUNK_INSTANCE)?;
        let subscriber = Subscriber {
            sequence: state.next_sequence,
            subscriber_id: input.subscriber_id,
            delivery_address: input.delivery_address,
        };
        if current_chunk.subscribers.len() == SUBSCRIBER_CHUNK_SIZE {
            let archive_token = archive_chunk_token(current_chunk.subscribers[0].sequence);
            SUBSCRIBER_CHUNKS.set(context, &archive_token, current_chunk)?;
            state.latest_archived_chunk_token = archive_token;
            current_chunk = SubscriberChunk::default();
        }
        current_chunk.subscribers.push(subscriber.clone());
        state.next_sequence += 1;
        SUBSCRIBER_CHUNKS.set(context, CURRENT_CHUNK_INSTANCE, current_chunk)?;
        ARCHIVE_STATE.set(context, state)?;
        Ok(RpcResult::new(subscriber))
    }

    fn get_subscriber_page(
        &self,
        context: &mut Context,
        input: GetSubscriberPageInput,
    ) -> HandlerResult<RpcResult<SubscriberPage>> {
        let page_token = validate_subscriber_page_token(&input.page_token)?;
        let chunk = SUBSCRIBER_CHUNKS
            .get(context, &page_token)?
            .ok_or_else(|| {
                HandlerError::new(
                    "ChunkedSubscriberFlow",
                    format!("subscriber page \"{page_token}\" not found"),
                )
            })?;
        let mut subscribers = chunk.subscribers;
        let mut next_page_token = String::new();
        if !subscribers.is_empty() {
            if page_token == CURRENT_CHUNK_INSTANCE {
                next_page_token = ARCHIVE_STATE
                    .get_required(context)?
                    .latest_archived_chunk_token;
            } else {
                let first_sequence = page_token.parse::<u64>().map_err(|error| {
                    HandlerError::new("ChunkedSubscriberFlow", error.to_string())
                })?;
                if first_sequence > 1 {
                    next_page_token =
                        archive_chunk_token(first_sequence - SUBSCRIBER_CHUNK_SIZE as u64);
                }
            }
        }
        subscribers.reverse();
        Ok(RpcResult::new(SubscriberPage {
            page_token,
            subscribers,
            next_page_token,
        }))
    }
}

impl Flow for ChunkedSubscriberFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&ARCHIVE_STATE)
            .attribute_map(&SUBSCRIBER_CHUNKS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(
                REGISTER_SUBSCRIBER
                    .lock(ARCHIVE_STATE.lock())
                    .lock(SUBSCRIBER_CHUNKS.lock(CURRENT_CHUNK_INSTANCE))
                    .load_attribute_map_instance(SUBSCRIBER_CHUNKS.load(CURRENT_CHUNK_INSTANCE)),
                Self::register_subscriber,
            )
            .function(GET_SUBSCRIBER_PAGE, Self::get_subscriber_page)
    }
}

pub fn validate_subscriber_page_token(page_token: &str) -> HandlerResult<String> {
    if page_token.is_empty() {
        return Ok(CURRENT_CHUNK_INSTANCE.to_owned());
    }
    if page_token == CURRENT_CHUNK_INSTANCE {
        return Ok(page_token.to_owned());
    }
    if page_token.len() != 20 || !page_token.bytes().all(|value| value.is_ascii_digit()) {
        return Err(invalid_page_token(page_token));
    }
    let first_sequence = page_token
        .parse::<u64>()
        .map_err(|_| invalid_page_token(page_token))?;
    if first_sequence == 0 || (first_sequence - 1) % SUBSCRIBER_CHUNK_SIZE as u64 != 0 {
        return Err(invalid_page_token(page_token));
    }
    Ok(page_token.to_owned())
}

pub fn archive_chunk_token(first_sequence: u64) -> String {
    format!("{first_sequence:020}")
}

fn invalid_page_token(page_token: &str) -> HandlerError {
    HandlerError::new(
        "ChunkedSubscriberFlow",
        format!("invalid page token \"{page_token}\""),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validates_subscriber_page_tokens() {
        assert_eq!(
            validate_subscriber_page_token("").expect("default token"),
            CURRENT_CHUNK_INSTANCE
        );
        assert_eq!(
            validate_subscriber_page_token("00000000000000000101").expect("archive token"),
            "00000000000000000101"
        );
        for page_token in ["1", "archive", "00000000000000000002"] {
            assert!(validate_subscriber_page_token(page_token).is_err());
        }
    }
}
