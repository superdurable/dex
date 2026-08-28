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
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const EMPLOYER_OPT_OUT: Rpc<(), ()> = Rpc::new("EmployerOptOut");
pub const EMPLOYER_IS_OPTED_IN: Rpc<(), bool> = Rpc::new("EmployerIsOptedIn");
pub const SHORTLIST_REVOKE: Rpc<(), ()> = Rpc::new("ShortlistRevoke");
pub const SHORTLIST_EMAIL_SENT_TIMESTAMP: Rpc<(), i64> = Rpc::new("ShortlistEmailSentTimestamp");

pub fn employer_opt_in_flow_id(employer_id: &str) -> String {
    format!("shortlist_candidates_opt_in_{employer_id}")
}

pub fn shortlist_flow_id(employer_id: &str, candidate_id: &str) -> String {
    format!("shortlist_candidates_shortlist_{employer_id}_{candidate_id}")
}

#[derive(Default)]
pub struct EmployerOptInFlow {
    opt_in: OptIn,
    await_opt_out: AwaitOptOut,
}

impl EmployerOptInFlow {
    fn opt_out(&self, context: &mut Context) -> HandlerResult<()> {
        OPT_OUT.publish(context, ())
    }

    fn is_opted_in(&self, _context: &mut Context) -> HandlerResult<RpcResult<bool>> {
        Ok(RpcResult::new(true))
    }
}

impl Flow for EmployerOptInFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.opt_in).and(&self.await_opt_out)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&EMPLOYER)
            .channel(&OPT_OUT)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(EMPLOYER_OPT_OUT, Self::opt_out)
            .function_without_input(EMPLOYER_IS_OPTED_IN, Self::is_opted_in)
    }
}

#[derive(Default)]
struct OptIn;

impl Step for OptIn {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        EMPLOYER.set(context, input)?;
        Ok(StepDecision::go_to(&AwaitOptOut, ()))
    }
}

#[derive(Default)]
struct AwaitOptOut;

impl Step for AwaitOptOut {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(OPT_OUT.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("opted-out".to_string()))
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ShortlistRequest {
    pub employer_id: String,
    pub candidate_id: String,
}

#[derive(Default)]
pub struct ShortlistFlow {
    schedule_contact: ScheduleContact,
}

impl ShortlistFlow {
    fn revoke(&self, context: &mut Context) -> HandlerResult<()> {
        REVOKED.publish(context, ())
    }

    fn email_sent_timestamp(&self, context: &mut Context) -> HandlerResult<RpcResult<i64>> {
        Ok(RpcResult::new(EMAIL_SENT.get(context)?.unwrap_or(0)))
    }
}

impl Flow for ShortlistFlow {
    type StartInput = ShortlistRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.schedule_contact)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&EMAIL_SENT)
            .channel(&REVOKED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(SHORTLIST_REVOKE, Self::revoke)
            .function_without_input(SHORTLIST_EMAIL_SENT_TIMESTAMP, Self::email_sent_timestamp)
    }
}

#[derive(Default)]
struct ScheduleContact;

impl Step for ScheduleContact {
    type Input = ShortlistRequest;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            REVOKED.for_one(),
            Timer::by_duration(Duration::from_secs(86_400)),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if REVOKED.condition_results(context)?.is_empty() {
            let timestamp = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|duration| duration.as_millis() as i64)
                .unwrap_or(0);
            EMAIL_SENT.set(context, timestamp)?;
            context.record_event("candidate-contact", input.candidate_id)?;
            Ok(StepDecision::graceful_complete("contacted".to_string()))
        } else {
            Ok(StepDecision::graceful_complete("revoked".to_string()))
        }
    }
}

static EMPLOYER: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("employer-opt-in"));

static OPT_OUT: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("employer-opt-out"));

static EMAIL_SENT: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("SHORTLIST_EmailSentTimestamp"));

static REVOKED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("shortlist-revoked"));
