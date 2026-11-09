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

//! Termination: Implicit Termination (WCP-11) and Explicit Termination
//! (WCP-43).

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, StepMovement, Wait,
};

static PROFILE_COMPLETED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("profile_completed"));

pub const COMPLETE_PROFILE: Rpc<(), ()> = Rpc::new("complete_profile");

/// WCP-11 Implicit Termination: GracefulComplete closes the Flow only once
/// no other Step is active.
#[derive(Default)]
pub struct ImplicitTerminationFlow {
    start_onboarding: StartOnboarding,
    create_account: CreateAccount,
    send_welcome_email: SendWelcomeEmail,
    await_profile_completion: AwaitProfileCompletion,
}

impl ImplicitTerminationFlow {
    fn complete_profile(&self, context: &mut Context) -> HandlerResult<()> {
        PROFILE_COMPLETED.publish(context, "completed".to_string())
    }
}

impl Flow for ImplicitTerminationFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_onboarding)
            .and(&self.create_account)
            .and(&self.send_welcome_email)
            .and(&self.await_profile_completion)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&PROFILE_COMPLETED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(COMPLETE_PROFILE, Self::complete_profile)
    }
}

#[derive(Default)]
struct StartOnboarding;

impl Step for StartOnboarding {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&CreateAccount, ()),
            StepMovement::to(&SendWelcomeEmail, ()),
            StepMovement::to(&AwaitProfileCompletion, ()),
        ]))
    }
}

#[derive(Default)]
pub struct CreateAccount;

impl Step for CreateAccount {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
pub struct SendWelcomeEmail;

impl Step for SendWelcomeEmail {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct AwaitProfileCompletion;

impl Step for AwaitProfileCompletion {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PROFILE_COMPLETED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

static DIAGNOSTICS_COLLECTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("diagnostics_collected"));
static ON_CALL_ACKNOWLEDGED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("on_call_acknowledged"));
static INCIDENT_RESOLVED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("incident_resolved"));

pub const RESOLVE_INCIDENT: Rpc<(), ()> = Rpc::new("resolve_incident");

/// WCP-43 Explicit Termination: ForceComplete closes the Flow at once and
/// cancels the Steps that are still waiting.
#[derive(Default)]
pub struct ExplicitTerminationFlow {
    start_incident: StartIncident,
    collect_diagnostics: CollectDiagnostics,
    notify_on_call: NotifyOnCall,
    resolve_incident: ResolveIncident,
}

impl ExplicitTerminationFlow {
    fn resolve_incident(&self, context: &mut Context) -> HandlerResult<()> {
        INCIDENT_RESOLVED.publish(context, "resolved".to_string())
    }
}

impl Flow for ExplicitTerminationFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_incident)
            .and(&self.collect_diagnostics)
            .and(&self.notify_on_call)
            .and(&self.resolve_incident)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&DIAGNOSTICS_COLLECTED)
            .channel(&ON_CALL_ACKNOWLEDGED)
            .channel(&INCIDENT_RESOLVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(RESOLVE_INCIDENT, Self::resolve_incident)
    }
}

#[derive(Default)]
pub struct StartIncident;

impl Step for StartIncident {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&CollectDiagnostics, ()),
            StepMovement::to(&NotifyOnCall, ()),
            StepMovement::to(&ResolveIncident, ()),
        ]))
    }
}

#[derive(Default)]
struct CollectDiagnostics;

impl Step for CollectDiagnostics {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(DIAGNOSTICS_COLLECTED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct NotifyOnCall;

impl Step for NotifyOnCall {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(ON_CALL_ACKNOWLEDGED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct ResolveIncident;

impl Step for ResolveIncident {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(INCIDENT_RESOLVED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_complete(self.step_type().to_string()))
    }
}
