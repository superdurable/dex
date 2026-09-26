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

use axum::{
    Json, Router,
    extract::{Query, State},
    response::IntoResponse,
    routing::{get, post},
};
use dex_sdk::{RpcInvokeOptions, StartFlowOptions};
use serde::Deserialize;

use crate::patterns::sequentially_chunked_attribute_map::flow::{
    ARCHIVE_STATE, CURRENT_CHUNK_INSTANCE, ChunkedSubscriberFlow, FLOW_ID, GET_SUBSCRIBER_PAGE,
    GetSubscriberPageInput, REGISTER_SUBSCRIBER, RegisterSubscriberInput, SUBSCRIBER_CHUNKS,
    SubscriberArchiveState, SubscriberChunk, validate_subscriber_page_token,
};
use crate::server::helpers::{SharedClient, StartResponse, map_sdk_error, ok_json, run_blocking};

#[derive(Deserialize)]
struct SubscriberPageQuery {
    #[serde(default, rename = "pageToken")]
    page_token: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route(
            "/patterns/sequentially-chunked-attribute-map/start",
            post(start),
        )
        .route(
            "/patterns/sequentially-chunked-attribute-map/register",
            post(register_subscriber),
        )
        .route(
            "/patterns/sequentially-chunked-attribute-map/subscribers",
            get(get_subscriber_page),
        )
        .with_state(client)
}

async fn start(State(client): State<SharedClient>) -> impl IntoResponse {
    match run_blocking(move || {
        let flow = ChunkedSubscriberFlow;
        client
            .start_flow_with_options(
                &flow,
                FLOW_ID,
                (),
                StartFlowOptions::new()
                    .ignore_already_started(true)
                    .initial_attribute(
                        &ARCHIVE_STATE,
                        SubscriberArchiveState {
                            next_sequence: 1,
                            latest_archived_chunk_token: String::new(),
                        },
                    )
                    .initial_attribute_map(
                        &SUBSCRIBER_CHUNKS,
                        CURRENT_CHUNK_INSTANCE,
                        SubscriberChunk::default(),
                    ),
            )
            .map(|run_id| StartResponse {
                flow_id: FLOW_ID.to_owned(),
                run_id,
            })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn register_subscriber(
    State(client): State<SharedClient>,
    Json(input): Json<RegisterSubscriberInput>,
) -> impl IntoResponse {
    match run_blocking(move || client.invoke_rpc(FLOW_ID, REGISTER_SUBSCRIBER, input)) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn get_subscriber_page(
    State(client): State<SharedClient>,
    Query(query): Query<SubscriberPageQuery>,
) -> impl IntoResponse {
    let page_token = match validate_subscriber_page_token(&query.page_token) {
        Ok(value) => value,
        Err(error) => return error.to_string().into_response(),
    };
    match run_blocking(move || {
        client.invoke_rpc_with_options(
            FLOW_ID,
            GET_SUBSCRIBER_PAGE,
            GetSubscriberPageInput {
                page_token: page_token.clone(),
            },
            RpcInvokeOptions::new().load_attribute_map_instance(SUBSCRIBER_CHUNKS.load(page_token)),
        )
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
