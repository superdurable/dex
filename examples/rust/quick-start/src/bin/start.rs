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

//! The Dex Quick Start in Rust: an order Flow that charges, waits on a 60-second Timer, and ships.
//!
//! Starts one OrderFlow. Pass an order ID, or let the command make one.

use std::error::Error;

use dex_quick_start::config::{QuickStartConfig, open_blob_cache, ship_delay_from_environment};
use dex_quick_start::order_flow::OrderFlow;
use dex_quick_start::shop_api::ShopApi;
use dex_sdk::{Client, ClientOptions, Registry};

fn main() -> Result<(), Box<dyn Error>> {
    let config = QuickStartConfig::from_environment();
    let order_id = std::env::args()
        .nth(1)
        .unwrap_or_else(|| format!("order-{:08x}", fastrand::u32(..)));
    let order_flow = OrderFlow::new(ShopApi::default());
    let registry = Registry::new().register(order_flow.clone())?;
    let cache = open_blob_cache(".dex-blob-cache/client")?;
    let options = ClientOptions::new().server_address(config.server_address);
    let client = Client::try_new(registry, cache, options)?;
    client.start_flow(&order_flow, &order_id, order_id.clone())?;
    let seconds = ship_delay_from_environment().as_secs();
    println!("Started Flow {order_id}. Ship runs in {seconds} seconds.");
    Ok(())
}
