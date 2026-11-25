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
    response::{IntoResponse, Response},
    routing::post,
};
use dex_sdk::StartFlowOptions;
use serde::{Deserialize, Serialize};

use crate::data_storage::entity_flow::flow::{
    ADJUST_CREDIT_BALANCE, CREDIT_BALANCE, ContactDetails, CreditAdjustment, CustomerAccountFlow,
    DISPLAY_NAME, EMAIL, RETIRE_ACCOUNT, UPDATE_CONTACT_DETAILS, customer_account_flow_id,
};
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, is_already_started, map_sdk_error, ok_json, ok_text,
    run_blocking,
};

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AccountRequest {
    pub account_id: String,
    #[serde(flatten)]
    pub contact: ContactDetails,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct CreditAdjustmentRequest {
    account_id: String,
    amount: i64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AccountQuery {
    account_id: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct AccountView {
    account_id: String,
    display_name: String,
    email: String,
    credit_balance: i64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct CreditBalanceResponse {
    credit_balance: i64,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route(
            "/patterns/entity-flow/account",
            post(open_account).get(get_account),
        )
        .route(
            "/patterns/entity-flow/account/contact",
            post(update_contact_details),
        )
        .route(
            "/patterns/entity-flow/account/credits",
            post(adjust_credit_balance),
        )
        .route("/patterns/entity-flow/account/retire", post(retire_account))
        .with_state(client)
}

pub fn open_account_options(contact: ContactDetails) -> StartFlowOptions {
    StartFlowOptions::new()
        .initial_attribute(&DISPLAY_NAME, contact.display_name)
        .initial_attribute(&EMAIL, contact.email)
        .initial_attribute(&CREDIT_BALANCE, 0_i64)
}

async fn open_account(
    State(client): State<SharedClient>,
    Json(request): Json<AccountRequest>,
) -> Response {
    if let Err(message) = validate_account_request(&request) {
        return error_response(StatusCode::BAD_REQUEST, &message);
    }
    let flow_id = customer_account_flow_id(&request.account_id);
    let started_flow_id = flow_id.clone();
    let result = run_blocking(move || {
        client.start_flow_with_options(
            &CustomerAccountFlow::default(),
            &started_flow_id,
            (),
            open_account_options(request.contact),
        )
    });
    match result {
        Ok(run_id) => ok_json(StartResponse { flow_id, run_id }),
        Err(error) if is_already_started(&error) => {
            error_response(StatusCode::CONFLICT, "account already exists")
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn update_contact_details(
    State(client): State<SharedClient>,
    Json(request): Json<AccountRequest>,
) -> Response {
    if let Err(message) = validate_account_request(&request) {
        return error_response(StatusCode::BAD_REQUEST, &message);
    }
    let flow_id = customer_account_flow_id(&request.account_id);
    match run_blocking(move || client.invoke_rpc(&flow_id, UPDATE_CONTACT_DETAILS, request.contact))
    {
        Ok(()) => ok_text("Updated contact details"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn adjust_credit_balance(
    State(client): State<SharedClient>,
    Json(request): Json<CreditAdjustmentRequest>,
) -> Response {
    let flow_id = customer_account_flow_id(&request.account_id);
    let adjustment = CreditAdjustment {
        amount: request.amount,
    };
    match run_blocking(move || client.invoke_rpc(&flow_id, ADJUST_CREDIT_BALANCE, adjustment)) {
        Ok(credit_balance) => ok_json(CreditBalanceResponse { credit_balance }),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn get_account(
    State(client): State<SharedClient>,
    Query(query): Query<AccountQuery>,
) -> Response {
    let flow_id = customer_account_flow_id(&query.account_id);
    match run_blocking(move || {
        client.get_attributes(&flow_id, (&DISPLAY_NAME, &EMAIL, &CREDIT_BALANCE))
    }) {
        Ok((display_name, email, credit_balance)) => ok_json(AccountView {
            account_id: query.account_id,
            display_name: display_name.unwrap_or_default(),
            email: email.unwrap_or_default(),
            credit_balance: credit_balance.unwrap_or_default(),
        }),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn retire_account(
    State(client): State<SharedClient>,
    Query(query): Query<AccountQuery>,
) -> Response {
    let flow_id = customer_account_flow_id(&query.account_id);
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, RETIRE_ACCOUNT)) {
        Ok(()) => ok_text("Retired account"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

fn validate_account_request(request: &AccountRequest) -> Result<(), String> {
    if request.account_id.trim().is_empty() {
        return Err("accountId is required".to_owned());
    }
    request
        .contact
        .validate()
        .map_err(|error| error.to_string())
}

fn error_response(status: StatusCode, message: &str) -> Response {
    (
        status,
        Json(ErrorBody {
            error: message.to_owned(),
        }),
    )
        .into_response()
}
