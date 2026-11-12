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

use std::collections::HashSet;
use std::sync::{Arc, Mutex};

use dex_sdk::{HandlerError, HandlerResult};

#[derive(Clone, Default)]
pub struct MyDependencyService {
    used_idempotency_keys: Arc<Mutex<HashSet<String>>>,
}

impl MyDependencyService {
    pub fn charge_user(&self, email: &str, customer_id: &str, amount: i64) {
        println!("charge user customerID[{customer_id}] email[{email}] for ${amount}");
    }

    pub fn send_email(&self, recipient: &str, subject: &str, content: &str) {
        println!("sending an email to {recipient}, title: {subject}, content: {content}");
    }

    pub fn ship_item(&self, order_id: &str, test_fail_at_shipping: bool) -> HandlerResult<()> {
        if test_fail_at_shipping {
            return Err(HandlerError::new(
                "ShipFailure",
                format!("ship failed for order {order_id}"),
            ));
        }
        println!("ship item {order_id}");
        Ok(())
    }

    pub fn update_external_system(&self, message: &str) {
        println!(
            "Update external system(like via RPC, or sending Kafka message or database): {message}"
        );
    }

    pub fn charge_user_with_idempotency_key(
        &self,
        email: &str,
        customer_id: &str,
        amount: i64,
        idempotency_key: &str,
    ) {
        if self.is_repeated_key(idempotency_key) {
            println!("charge already done (key {idempotency_key}); not charged again");
            return;
        }
        self.charge_user(email, customer_id, amount);
    }

    pub fn ship_item_with_idempotency_key(
        &self,
        order_id: &str,
        test_fail_at_shipping: bool,
        idempotency_key: &str,
    ) -> HandlerResult<()> {
        if test_fail_at_shipping {
            return self.ship_item(order_id, true);
        }
        if self.is_repeated_key(idempotency_key) {
            println!("ship already done (key {idempotency_key}); not shipped again");
            return Ok(());
        }
        self.ship_item(order_id, false)
    }

    pub fn update_external_system_with_idempotency_key(
        &self,
        message: &str,
        idempotency_key: &str,
    ) {
        if self.is_repeated_key(idempotency_key) {
            println!("already done (key {idempotency_key}); not sent again: {message}");
            return;
        }
        self.update_external_system(message);
    }

    /// Reports whether the key was used before, and records it.
    fn is_repeated_key(&self, idempotency_key: &str) -> bool {
        let mut used_keys = self
            .used_idempotency_keys
            .lock()
            .expect("no holder of the key set panics while holding it");
        !used_keys.insert(idempotency_key.to_owned())
    }
}
