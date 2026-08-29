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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use std::time::Duration;

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const ENGAGEMENT_DESCRIBE: Rpc<(), EngagementStatus> = Rpc::new("EngagementDescribe");
pub const ENGAGEMENT_ACCEPT: Rpc<String, ()> = Rpc::new("EngagementAccept");
pub const ENGAGEMENT_DECLINE: Rpc<String, ()> = Rpc::new("EngagementDecline");
pub const ENGAGEMENT_OPT_OUT: Rpc<(), ()> = Rpc::new("EngagementOptOut");

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct EngagementRequest {
    pub employer_id: String,
    pub candidate_id: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct EngagementStatus {
    pub status: String,
    pub notes: String,
}

#[derive(Default)]
pub struct EngagementFlow {
    start: Start,
    wait_for_decision: WaitForDecision,
    notify_external_system: NotifyExternalSystem,
}

impl EngagementFlow {
    fn describe(&self, context: &mut Context) -> HandlerResult<RpcResult<EngagementStatus>> {
        Ok(RpcResult::new(STATUS.get(context)?.unwrap_or_default()))
    }

    fn accept(&self, context: &mut Context, notes: String) -> HandlerResult<()> {
        DECISION.publish(
            context,
            EngagementStatus {
                status: "accepted".into(),
                notes,
            },
        )
    }

    fn decline(&self, context: &mut Context, notes: String) -> HandlerResult<()> {
        DECISION.publish(
            context,
            EngagementStatus {
                status: "declined".into(),
                notes,
            },
        )
    }

    fn opt_out(&self, context: &mut Context) -> HandlerResult<()> {
        DECISION.publish(
            context,
            EngagementStatus {
                status: "opted-out".into(),
                notes: String::new(),
            },
        )
    }
}

impl Flow for EngagementFlow {
    type StartInput = EngagementRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.wait_for_decision)
            .and(&self.notify_external_system)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STATUS)
            .attribute(&ENGAGEMENT_STATUS)
            .channel(&DECISION)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(ENGAGEMENT_DESCRIBE, Self::describe)
            .procedure(ENGAGEMENT_ACCEPT, Self::accept)
            .procedure(ENGAGEMENT_DECLINE, Self::decline)
            .procedure_without_input(ENGAGEMENT_OPT_OUT, Self::opt_out)
    }
}

#[derive(Default)]
struct Start;

impl Step for Start {
    type Input = EngagementRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        STATUS.set(
            context,
            EngagementStatus {
                status: "pending".into(),
                notes: String::new(),
            },
        )?;
        ENGAGEMENT_STATUS.set(context, "pending".to_owned())?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&WaitForDecision, input.clone()),
            StepMovement::to(
                &NotifyExternalSystem,
                format!("started:{}", input.candidate_id),
            ),
        ]))
    }
}

#[derive(Default)]
struct WaitForDecision;

impl Step for WaitForDecision {
    type Input = EngagementRequest;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            DECISION.for_one(),
            Timer::by_duration(Duration::from_secs(86_400)),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let resolved = DECISION
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or(EngagementStatus {
                status: "reminder-due".into(),
                notes: String::new(),
            });
        STATUS.set(context, resolved.clone())?;
        ENGAGEMENT_STATUS.set(context, resolved.status.clone())?;
        Ok(StepDecision::go_to(&NotifyExternalSystem, resolved.status))
    }
}

#[derive(Default)]
struct NotifyExternalSystem;

impl Step for NotifyExternalSystem {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("engagement-notification", input)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

static STATUS: LazyLock<Attribute<EngagementStatus>> =
    LazyLock::new(|| Attribute::new("engagement-status").indexed(AttributeIndex::keyword()));

pub static ENGAGEMENT_STATUS: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("engagement-status-value").indexed(AttributeIndex::keyword()));

static DECISION: LazyLock<Channel<EngagementStatus>> =
    LazyLock::new(|| Channel::new("engagement-decision"));
