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

//! A mock payment and shipping API that records each call.

use std::sync::{Arc, Mutex};

/// Clones share one record of calls, so a test can inspect what a Worker did.
#[derive(Clone, Default)]
pub struct ShopApi {
    calls: Arc<Mutex<ShopCalls>>,
}

#[derive(Default)]
struct ShopCalls {
    charged: Vec<String>,
    shipped: Vec<String>,
}

impl ShopApi {
    pub fn charge(&self, order_id: &str) {
        self.record().charged.push(order_id.to_string());
        println!("Charged {order_id}");
    }

    pub fn ship(&self, order_id: &str) {
        self.record().shipped.push(order_id.to_string());
        println!("Shipped {order_id}");
    }

    pub fn charged(&self) -> Vec<String> {
        self.record().charged.clone()
    }

    pub fn shipped(&self) -> Vec<String> {
        self.record().shipped.clone()
    }

    fn record(&self) -> std::sync::MutexGuard<'_, ShopCalls> {
        self.calls
            .lock()
            .expect("no thread panics while holding the call record")
    }
}
