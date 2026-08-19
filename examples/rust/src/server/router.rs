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

use tower_http::cors::CorsLayer;
use tower_http::trace::TraceLayer;

use crate::patterns;
use crate::primitives;
use crate::products;
use crate::server::helpers::SharedClient;

pub fn build_router(client: SharedClient) -> axum::Router {
    axum::Router::new()
        .merge(products::engagement::controller::mount(client.clone()))
        .merge(products::job_post::controller::mount(client.clone()))
        .merge(products::microservices::controller::mount(client.clone()))
        .merge(products::money_transfer::controller::mount(client.clone()))
        .merge(products::order_processing::controller::mount(
            client.clone(),
            products::order_processing::OrderProcessingFlow::new(
                crate::shared::MyDependencyService,
            ),
        ))
        .merge(products::polling::controller::mount(client.clone()))
        .merge(products::shortlist_candidates::controller::mount(
            client.clone(),
        ))
        .merge(products::signup::controller::mount(client.clone()))
        .merge(products::subscription::controller::mount(client.clone()))
        .merge(patterns::polling::controller::mount(client.clone()))
        .merge(patterns::interruptible::controller::mount(client.clone()))
        .merge(patterns::reminders::controller::mount(client.clone()))
        .merge(patterns::entity_store::controller::mount(client.clone()))
        .merge(patterns::intervention::controller::mount(client.clone()))
        .merge(patterns::resettable_timer::controller::mount(
            client.clone(),
        ))
        .merge(patterns::parallel::controller::mount(client.clone()))
        .merge(patterns::recovery::controller::mount(client.clone()))
        .merge(patterns::scalable_parallel::controller::mount(
            client.clone(),
        ))
        .merge(patterns::parent_child::controller::mount(client.clone()))
        .merge(patterns::drain_channels::controller::mount(client.clone()))
        .merge(patterns::wait_for_state_completion::controller::mount(
            client.clone(),
        ))
        .merge(patterns::timeout::controller::mount(client.clone()))
        .merge(primitives::step::controller::mount(client.clone()))
        .merge(primitives::attribute::controller::mount(client.clone()))
        .merge(primitives::channel::controller::mount(client.clone()))
        .merge(primitives::timer::controller::mount(client.clone()))
        .merge(primitives::rpc::controller::mount(client.clone()))
        .merge(primitives::subflow::controller::mount(client.clone()))
        .merge(primitives::client_apis::controller::mount(client))
        .layer(TraceLayer::new_for_http())
        .layer(CorsLayer::permissive())
}
