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

//! A mock payment and shipping API that drops a repeated idempotency key.

use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

/// Clones share one record of calls, so a test can inspect what a Worker did.
/// With a keys path, it remembers keys across processes.
#[derive(Clone, Default)]
pub struct ShopApi {
    calls: Arc<Mutex<ShopCalls>>,
    keys_path: Option<PathBuf>,
    charge_delay: Duration,
}

#[derive(Default)]
struct ShopCalls {
    used_keys: BTreeSet<String>,
    charged: Vec<String>,
    shipped: Vec<String>,
    repeated_keys: Vec<String>,
}

impl ShopApi {
    /// Keeps keys in `keys_path` and waits `charge_delay` after each new charge.
    pub fn with_keys_file(keys_path: impl Into<PathBuf>, charge_delay: Duration) -> Self {
        Self {
            calls: Arc::default(),
            keys_path: Some(keys_path.into()),
            charge_delay,
        }
    }

    pub fn charge(&self, order_id: &str, idempotency_key: &str) -> std::io::Result<()> {
        if self.is_repeat(idempotency_key)? {
            println!(
                "Charge for {order_id} already done (key {idempotency_key}); not charged again"
            );
            return Ok(());
        }
        self.record().charged.push(order_id.to_string());
        println!("Charged {order_id} with key {idempotency_key}");
        // Leaves time to stop the Worker after the payment, before Dex records the Step.
        std::thread::sleep(self.charge_delay);
        Ok(())
    }

    pub fn ship(&self, order_id: &str, idempotency_key: &str) -> std::io::Result<()> {
        if self.is_repeat(idempotency_key)? {
            println!("Ship for {order_id} already done (key {idempotency_key}); not shipped again");
            return Ok(());
        }
        self.record().shipped.push(order_id.to_string());
        println!("Shipped {order_id} with key {idempotency_key}");
        Ok(())
    }

    pub fn charged(&self) -> Vec<String> {
        self.record().charged.clone()
    }

    pub fn shipped(&self) -> Vec<String> {
        self.record().shipped.clone()
    }

    pub fn repeated_keys(&self) -> Vec<String> {
        self.record().repeated_keys.clone()
    }

    fn is_repeat(&self, idempotency_key: &str) -> std::io::Result<bool> {
        let mut calls = self.record();
        if let Some(keys_path) = &self.keys_path
            && keys_path.exists()
        {
            let saved = std::fs::read_to_string(keys_path)?;
            calls
                .used_keys
                .extend(saved.split_whitespace().map(str::to_string));
        }
        if calls.used_keys.contains(idempotency_key) {
            calls.repeated_keys.push(idempotency_key.to_string());
            return Ok(true);
        }
        calls.used_keys.insert(idempotency_key.to_string());
        if let Some(keys_path) = &self.keys_path {
            let lines: String = calls
                .used_keys
                .iter()
                .map(|key| format!("{key}\n"))
                .collect();
            std::fs::write(keys_path, lines)?;
        }
        Ok(false)
    }

    fn record(&self) -> std::sync::MutexGuard<'_, ShopCalls> {
        self.calls
            .lock()
            .expect("no thread panics while holding the call record")
    }
}
