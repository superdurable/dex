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
use serde::Deserialize;
use serde_json::json;

use crate::patterns::entity_store::flow::{
    USER_PROFILE_CLEAR, USER_PROFILE_READ, USER_PROFILE_UPDATE, UserProfile, UserProfileFlow,
    UserProfileMetadata,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct ProfileQuery {
    #[serde(default, rename = "userId")]
    user_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct UserProfileRequest {
    user_id: String,
    #[serde(flatten)]
    profile: UserProfile,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/entity-store/start", get(start))
        .route("/patterns/entity-store/profile", post(create_profile))
        .route("/patterns/entity-store/profile", get(get_profile))
        .route(
            "/patterns/entity-store/profile/update",
            post(update_profile),
        )
        .route("/patterns/entity-store/profile/clear", post(clear_profile))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("entity")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = UserProfileFlow;
        let profile = default_profile();
        client
            .start_flow_with_options(
                &flow,
                &flow_id,
                (),
                UserProfileFlow::start_options(&profile),
            )
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn create_profile(
    State(client): State<SharedClient>,
    Json(request): Json<UserProfileRequest>,
) -> impl IntoResponse {
    let user_id = request.user_id;
    let profile = request.profile;
    match run_blocking(move || {
        let flow = UserProfileFlow;
        client
            .start_flow_with_options(
                &flow,
                &user_id,
                (),
                UserProfileFlow::start_options(&profile),
            )
            .map(|run_id| {
                json!({
                    "flowID": user_id,
                    "runID": run_id,
                    "userId": user_id,
                })
            })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn update_profile(
    State(client): State<SharedClient>,
    Json(request): Json<UserProfileRequest>,
) -> impl IntoResponse {
    let user_id = request.user_id;
    let profile = request.profile;
    match run_blocking(move || client.invoke_rpc(&user_id, USER_PROFILE_UPDATE, profile)) {
        Ok(()) => ok_text("Updated user profile"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn get_profile(
    State(client): State<SharedClient>,
    Query(query): Query<ProfileQuery>,
) -> impl IntoResponse {
    let user_id = query.user_id;
    match run_blocking(move || client.invoke_rpc_without_input(&user_id, USER_PROFILE_READ)) {
        Ok(profile) => ok_json(profile),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn clear_profile(
    State(client): State<SharedClient>,
    Query(query): Query<ProfileQuery>,
) -> impl IntoResponse {
    let user_id = query.user_id;
    match run_blocking(move || client.invoke_rpc_without_input(&user_id, USER_PROFILE_CLEAR)) {
        Ok(()) => ok_text("Cleared user profile"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

fn default_profile() -> UserProfile {
    UserProfile {
        display_name: "Ada Lovelace".into(),
        email: "ada@example.com".into(),
        marketing_opt_in: true,
        credits: 100,
        weight: 61.5,
        last_logged_in_time: "2026-01-15T09:30:00Z".into(),
        metadata: UserProfileMetadata {
            source: "playground".into(),
            tags: vec!["example".into()],
        },
    }
}
