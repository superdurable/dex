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
    http::StatusCode,
    response::IntoResponse,
    routing::{get, post},
};
use serde::Deserialize;

use crate::products::shortlist_candidates::flow::{
    EMPLOYER_IS_OPTED_IN, EMPLOYER_OPT_OUT, EmployerOptInFlow, SHORTLIST_EMAIL_SENT_TIMESTAMP,
    SHORTLIST_REVOKE, ShortlistFlow, ShortlistRequest, employer_opt_in_flow_id, shortlist_flow_id,
};
use crate::server::helpers::{
    SharedClient, StartResponse, is_already_started, is_missing_or_inactive, map_sdk_error,
    new_flow_id, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct EmployerBody {
    #[serde(default)]
    employer_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct ShortlistBody {
    #[serde(default)]
    employer_id: String,
    #[serde(default)]
    candidate_id: String,
}

#[derive(Deserialize)]
struct EmployerQuery {
    #[serde(default, rename = "employerId")]
    employer_id: String,
}

#[derive(Deserialize)]
struct EmailQuery {
    #[serde(default, rename = "employerId")]
    employer_id: String,
    #[serde(default, rename = "candidateId")]
    candidate_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/shortlist-candidates/start", get(start))
        .route("/products/shortlist-candidates/opt_in", post(opt_in))
        .route("/products/shortlist-candidates/opt_out", post(opt_out))
        .route(
            "/products/shortlist-candidates/is_opted_in",
            get(is_opted_in),
        )
        .route("/products/shortlist-candidates/shortlist", post(shortlist))
        .route(
            "/products/shortlist-candidates/revoke_shortlist",
            post(revoke_shortlist),
        )
        .route(
            "/products/shortlist-candidates/email_sent_timestamp",
            get(email_sent_timestamp),
        )
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("shortlist")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = ShortlistFlow::default();
        let input = ShortlistRequest {
            employer_id: "employer".into(),
            candidate_id: "candidate".into(),
        };
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn opt_in(
    State(client): State<SharedClient>,
    Json(body): Json<EmployerBody>,
) -> impl IntoResponse {
    let employer_id = body.employer_id;
    let flow_id = employer_opt_in_flow_id(&employer_id);
    let started_id = flow_id.clone();
    let started_employer = employer_id.clone();
    match run_blocking(move || {
        let flow = EmployerOptInFlow::default();
        client.start_flow(&flow, &started_id, started_employer)
    }) {
        Ok(_) => ok_text(format!("Started workflowId: {flow_id}")),
        Err(error) if is_already_started(&error) => {
            ok_text(format!("Employer {employer_id} has already opted in"))
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn opt_out(
    State(client): State<SharedClient>,
    Json(body): Json<EmployerBody>,
) -> impl IntoResponse {
    let employer_id = body.employer_id;
    let flow_id = employer_opt_in_flow_id(&employer_id);
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, EMPLOYER_OPT_OUT)) {
        Ok(()) => ok_text(format!("Employer {employer_id} has opted out")),
        Err(error) if is_missing_or_inactive(&error) => ok_text(format!(
            "Employer {employer_id} is not in the opt-in status"
        )),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn is_opted_in(
    State(client): State<SharedClient>,
    Query(query): Query<EmployerQuery>,
) -> impl IntoResponse {
    let employer_id = if query.employer_id.is_empty() {
        "test-employer".to_string()
    } else {
        query.employer_id
    };
    let flow_id = employer_opt_in_flow_id(&employer_id);
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, EMPLOYER_IS_OPTED_IN)) {
        Ok(opted_in) => ok_json(opted_in),
        Err(error) if is_missing_or_inactive(&error) => ok_json(false),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn shortlist(
    State(client): State<SharedClient>,
    Json(body): Json<ShortlistBody>,
) -> impl IntoResponse {
    let employer_id = body.employer_id;
    let candidate_id = body.candidate_id;
    let opt_in_id = employer_opt_in_flow_id(&employer_id);
    let flow_id = shortlist_flow_id(&employer_id, &candidate_id);
    let missing_opt_in = format!("Do nothing for {employer_id} because of no opt-in");
    match run_blocking(move || {
        let opted_in = match client.invoke_rpc_without_input(&opt_in_id, EMPLOYER_IS_OPTED_IN) {
            Ok(value) => value,
            Err(error) if is_missing_or_inactive(&error) => false,
            Err(error) => return Err(error),
        };
        if !opted_in {
            return Ok(None);
        }
        let flow = ShortlistFlow::default();
        let input = ShortlistRequest {
            employer_id,
            candidate_id,
        };
        match client.start_flow(&flow, &flow_id, input) {
            Ok(_) => Ok(Some(format!("Started workflowId: {flow_id}"))),
            Err(error) if is_already_started(&error) => {
                Ok(Some(format!("Already running workflowId: {flow_id}")))
            }
            Err(error) => Err(error),
        }
    }) {
        Ok(None) => ok_text(missing_opt_in),
        Ok(Some(message)) => ok_text(message),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn revoke_shortlist(
    State(client): State<SharedClient>,
    Json(body): Json<ShortlistBody>,
) -> impl IntoResponse {
    let employer_id = body.employer_id;
    let candidate_id = body.candidate_id;
    let flow_id = shortlist_flow_id(&employer_id, &candidate_id);
    let revoked = format!("Revoked shortlist for {employer_id}-{candidate_id}");
    let missing = format!("No running workflow to revoke for {employer_id}-{candidate_id}");
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, SHORTLIST_REVOKE)) {
        Ok(()) => ok_text(revoked),
        Err(error) if is_missing_or_inactive(&error) => ok_text(missing),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn email_sent_timestamp(
    State(client): State<SharedClient>,
    Query(query): Query<EmailQuery>,
) -> impl IntoResponse {
    let employer_id = if query.employer_id.is_empty() {
        "test-employer".to_string()
    } else {
        query.employer_id
    };
    let candidate_id = if query.candidate_id.is_empty() {
        "test-candidate".to_string()
    } else {
        query.candidate_id
    };
    let flow_id = shortlist_flow_id(&employer_id, &candidate_id);
    match run_blocking(move || {
        client.invoke_rpc_without_input(&flow_id, SHORTLIST_EMAIL_SENT_TIMESTAMP)
    }) {
        Ok(timestamp) => ok_json(timestamp),
        Err(error) if is_missing_or_inactive(&error) => StatusCode::NOT_FOUND.into_response(),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
