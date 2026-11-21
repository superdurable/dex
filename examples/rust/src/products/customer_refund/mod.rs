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

//! Customer refund: check a refund request, ask a manager when needed, refund, and tell the customer.

pub mod billing_service;
pub mod email_service;
mod json_ledger;
pub mod message_model;
pub mod refund_config;
pub mod refund_flow;
pub mod refund_scenarios;
pub mod refund_worker;
