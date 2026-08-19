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

use dex_sdk::{HandlerError, HandlerResult};

#[derive(Clone, Default)]
pub struct MyDependencyService;

impl MyDependencyService {
    pub fn charge_user(&self, email: &str, customer_id: &str, amount: i64) {
        println!("charge user customerID[{customer_id}] email[{email}] for ${amount}");
    }

    pub fn send_email(&self, recipient: &str, subject: &str, content: &str) {
        println!("sending an email to {recipient}, title: {subject}, content: {content}");
    }

    pub fn ship_item(&self, order_id: &str, test_fail_at_shipping: bool) -> HandlerResult<()> {
        if test_fail_at_shipping {
            return Err(HandlerError::new(format!(
                "ship failed for order {order_id}"
            )));
        }
        println!("ship item {order_id}");
        Ok(())
    }

    pub fn update_external_system(&self, message: &str) {
        println!(
            "Update external system(like via RPC, or sending Kafka message or database): {message}"
        );
    }
}
