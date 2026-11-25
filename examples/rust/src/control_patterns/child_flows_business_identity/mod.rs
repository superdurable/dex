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

//! Child Flows with Business Identity: an order starts one top-level
//! shipment Flow per shipment, named by its shipment number.

pub mod controller;
pub mod order_fulfillment_flow;
pub mod shipment_flow;

pub use order_fulfillment_flow::*;
pub use shipment_flow::*;
