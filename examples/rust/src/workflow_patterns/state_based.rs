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

//! State-based patterns: Deferred Choice (WCP-16), Interleaved Parallel
//! Routing (WCP-17), Milestone (WCP-18), Critical Section (WCP-39), and
//! Interleaved Routing (WCP-40).

use std::sync::LazyLock;
use std::thread;
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, StepMovement, StepOptions, Wait,
};

use super::{TaskInterval, current_unix_millis};

/// How long an interleaved task holds the shared lock, so overlaps would show.
const INTERLEAVED_TASK_DURATION: Duration = Duration::from_millis(200);
/// How long each Step of a critical section holds the token.
const CRITICAL_SECTION_STEP_DURATION: Duration = Duration::from_millis(100);

static OFFER_PRESENTED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("offerPresented"));
static OFFER_ACCEPTED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("offerAccepted"));
static OFFER_DECLINED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("offerDeclined"));

pub const PRESENT_OFFER: Rpc<(), ()> = Rpc::new("present_offer");
pub const ACCEPT_OFFER: Rpc<(), ()> = Rpc::new("accept_offer");
pub const DECLINE_OFFER: Rpc<(), ()> = Rpc::new("decline_offer");

/// WCP-16 Deferred Choice: the first customer response selects the branch,
/// and Dex consumes only the selected Channel message.
#[derive(Default)]
pub struct DeferredChoiceFlow {
    present_offer: PresentOffer,
    await_customer_response: AwaitCustomerResponse,
    fulfill_offer: FulfillOffer,
    archive_offer: ArchiveOffer,
}

impl DeferredChoiceFlow {
    fn present_offer(&self, context: &mut Context) -> HandlerResult<()> {
        OFFER_PRESENTED.publish(context, "presented".to_string())
    }

    fn accept_offer(&self, context: &mut Context) -> HandlerResult<()> {
        OFFER_ACCEPTED.publish(context, "accepted".to_string())
    }

    fn decline_offer(&self, context: &mut Context) -> HandlerResult<()> {
        OFFER_DECLINED.publish(context, "declined".to_string())
    }
}

impl Flow for DeferredChoiceFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.present_offer)
            .and(&self.await_customer_response)
            .and(&self.fulfill_offer)
            .and(&self.archive_offer)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&OFFER_PRESENTED)
            .channel(&OFFER_ACCEPTED)
            .channel(&OFFER_DECLINED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(PRESENT_OFFER, Self::present_offer)
            .procedure_without_input(ACCEPT_OFFER, Self::accept_offer)
            .procedure_without_input(DECLINE_OFFER, Self::decline_offer)
    }
}

#[derive(Default)]
pub struct PresentOffer;

impl Step for PresentOffer {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(OFFER_PRESENTED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AwaitCustomerResponse, ()))
    }
}

#[derive(Default)]
struct AwaitCustomerResponse;

impl Step for AwaitCustomerResponse {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            OFFER_ACCEPTED.for_one(),
            OFFER_DECLINED.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        if OFFER_ACCEPTED.condition_results(context)?.is_empty() {
            return Ok(StepDecision::go_to(&ArchiveOffer, ()));
        }
        Ok(StepDecision::go_to(&FulfillOffer, ()))
    }
}

#[derive(Default)]
struct FulfillOffer;

impl Step for FulfillOffer {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct ArchiveOffer;

impl Step for ArchiveOffer {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

static ONBOARDING_TASK_LOCK: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("onboardingTaskLock"));
static ONBOARDING_TASK_FINISHED: LazyLock<Channel<TaskInterval>> =
    LazyLock::new(|| Channel::new("onboardingTaskFinished"));

const ONBOARDING_TASK_COUNT: usize = 3;

/// WCP-17 Interleaved Parallel Routing: every task holds the same Execute
/// lock, so no two run at once, and GoTo keeps the partial order.
#[derive(Default)]
pub struct InterleavedParallelRoutingFlow {
    start_onboarding_tasks: StartOnboardingTasks,
    prepare_contract: PrepareContract,
    sign_contract: SignContract,
    run_credit_check: RunCreditCheck,
    complete_onboarding_tasks: CompleteOnboardingTasks,
}

impl Flow for InterleavedParallelRoutingFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_onboarding_tasks)
            .and(&self.prepare_contract)
            .and(&self.sign_contract)
            .and(&self.run_credit_check)
            .and(&self.complete_onboarding_tasks)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&ONBOARDING_TASK_LOCK)
            .channel(&ONBOARDING_TASK_FINISHED)
    }
}

#[derive(Default)]
struct StartOnboardingTasks;

impl Step for StartOnboardingTasks {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&PrepareContract, ()),
            StepMovement::to(&RunCreditCheck, ()),
            StepMovement::to(&CompleteOnboardingTasks, ONBOARDING_TASK_COUNT),
        ]))
    }
}

#[derive(Default)]
struct PrepareContract;

impl Step for PrepareContract {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(ONBOARDING_TASK_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        run_interleaved_task(context, &ONBOARDING_TASK_FINISHED, self.step_type())?;
        Ok(StepDecision::go_to(&SignContract, ()))
    }
}

#[derive(Default)]
struct SignContract;

impl Step for SignContract {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(ONBOARDING_TASK_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        run_interleaved_task(context, &ONBOARDING_TASK_FINISHED, self.step_type())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct RunCreditCheck;

impl Step for RunCreditCheck {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(ONBOARDING_TASK_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        run_interleaved_task(context, &ONBOARDING_TASK_FINISHED, self.step_type())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CompleteOnboardingTasks;

impl Step for CompleteOnboardingTasks {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, task_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(ONBOARDING_TASK_FINISHED.for_n(task_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _task_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let intervals = ONBOARDING_TASK_FINISHED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(intervals))
    }
}

pub static IS_CHANGE_WINDOW_OPEN: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("isChangeWindowOpen"));
static RELEASE_APPROVED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("releaseApproved"));
static RELEASE_FINALIZED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("releaseFinalized"));

pub const REQUEST_CONFIG_CHANGE: Rpc<String, bool> = Rpc::new("request_config_change");
pub const APPROVE_RELEASE: Rpc<(), ()> = Rpc::new("approve_release");
pub const FINALIZE_RELEASE: Rpc<(), ()> = Rpc::new("finalize_release");

/// WCP-18 Milestone: a config change can start only while the change window
/// Attribute marks the milestone as reached and not yet passed.
#[derive(Default)]
pub struct MilestoneFlow {
    open_change_window: OpenChangeWindow,
    await_release_approval: AwaitReleaseApproval,
    await_release_finalization: AwaitReleaseFinalization,
    apply_config_change: ApplyConfigChange,
}

impl MilestoneFlow {
    fn request_config_change(
        &self,
        context: &mut Context,
        change: String,
    ) -> HandlerResult<RpcResult<bool>> {
        if !IS_CHANGE_WINDOW_OPEN.get(context)?.unwrap_or(false) {
            return Ok(RpcResult::new(false));
        }
        Ok(RpcResult::new(true).then(StepMovement::to(&ApplyConfigChange, change)))
    }

    fn approve_release(&self, context: &mut Context) -> HandlerResult<()> {
        RELEASE_APPROVED.publish(context, "approved".to_string())
    }

    fn finalize_release(&self, context: &mut Context) -> HandlerResult<()> {
        RELEASE_FINALIZED.publish(context, "finalized".to_string())
    }
}

impl Flow for MilestoneFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.open_change_window)
            .and(&self.await_release_approval)
            .and(&self.await_release_finalization)
            .and(&self.apply_config_change)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&IS_CHANGE_WINDOW_OPEN)
            .channel(&RELEASE_APPROVED)
            .channel(&RELEASE_FINALIZED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(REQUEST_CONFIG_CHANGE, Self::request_config_change)
            .procedure_without_input(APPROVE_RELEASE, Self::approve_release)
            .procedure_without_input(FINALIZE_RELEASE, Self::finalize_release)
    }
}

#[derive(Default)]
struct OpenChangeWindow;

impl Step for OpenChangeWindow {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        IS_CHANGE_WINDOW_OPEN.set(context, true)?;
        Ok(StepDecision::go_to(&AwaitReleaseApproval, ()))
    }
}

#[derive(Default)]
struct AwaitReleaseApproval;

impl Step for AwaitReleaseApproval {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(RELEASE_APPROVED.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        IS_CHANGE_WINDOW_OPEN.set(context, false)?;
        Ok(StepDecision::go_to(&AwaitReleaseFinalization, ()))
    }
}

#[derive(Default)]
struct AwaitReleaseFinalization;

impl Step for AwaitReleaseFinalization {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(RELEASE_FINALIZED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            self.step_type().to_string(),
        ))
    }
}

#[derive(Default)]
struct ApplyConfigChange;

impl Step for ApplyConfigChange {
    type Input = String;

    fn execute(&self, _context: &mut Context, change: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(change))
    }
}

static CRITICAL_SECTION_TOKEN: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("criticalSectionToken"));
static CRITICAL_SECTION_FINISHED: LazyLock<Channel<TaskInterval>> =
    LazyLock::new(|| Channel::new("criticalSectionFinished"));

const CRITICAL_SECTION_COUNT: usize = 2;
const INVENTORY_SECTION: &str = "inventory";
const PRICING_SECTION: &str = "pricing";

/// WCP-39 Critical Section: a single token message guards a section that
/// spans two Steps, which an Attribute lock on one Step method cannot do.
#[derive(Default)]
pub struct CriticalSectionFlow {
    start_critical_sections: StartCriticalSections,
    reserve_stock: ReserveStock,
    commit_stock: CommitStock,
    recalculate_price: RecalculatePrice,
    publish_price: PublishPrice,
    finish_critical_sections: FinishCriticalSections,
}

impl Flow for CriticalSectionFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_critical_sections)
            .and(&self.reserve_stock)
            .and(&self.commit_stock)
            .and(&self.recalculate_price)
            .and(&self.publish_price)
            .and(&self.finish_critical_sections)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&CRITICAL_SECTION_TOKEN)
            .channel(&CRITICAL_SECTION_FINISHED)
    }
}

#[derive(Default)]
struct StartCriticalSections;

impl Step for StartCriticalSections {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        CRITICAL_SECTION_TOKEN.publish(context, "token".to_string())?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&ReserveStock, ()),
            StepMovement::to(&RecalculatePrice, ()),
            StepMovement::to(&FinishCriticalSections, CRITICAL_SECTION_COUNT),
        ]))
    }
}

#[derive(Default)]
struct ReserveStock;

impl Step for ReserveStock {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CRITICAL_SECTION_TOKEN.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let started_at_millis = enter_critical_section()?;
        Ok(StepDecision::go_to(&CommitStock, started_at_millis))
    }
}

#[derive(Default)]
struct CommitStock;

impl Step for CommitStock {
    type Input = i64;

    fn execute(
        &self,
        context: &mut Context,
        started_at_millis: Self::Input,
    ) -> HandlerResult<StepDecision> {
        leave_critical_section(context, INVENTORY_SECTION, started_at_millis)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct RecalculatePrice;

impl Step for RecalculatePrice {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CRITICAL_SECTION_TOKEN.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let started_at_millis = enter_critical_section()?;
        Ok(StepDecision::go_to(&PublishPrice, started_at_millis))
    }
}

#[derive(Default)]
struct PublishPrice;

impl Step for PublishPrice {
    type Input = i64;

    fn execute(
        &self,
        context: &mut Context,
        started_at_millis: Self::Input,
    ) -> HandlerResult<StepDecision> {
        leave_critical_section(context, PRICING_SECTION, started_at_millis)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct FinishCriticalSections;

impl Step for FinishCriticalSections {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, section_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CRITICAL_SECTION_FINISHED.for_n(section_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _section_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let intervals = CRITICAL_SECTION_FINISHED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(intervals))
    }
}

/// Records when the section started, then does its first half of work.
fn enter_critical_section() -> HandlerResult<i64> {
    let started_at_millis = current_unix_millis()?;
    thread::sleep(CRITICAL_SECTION_STEP_DURATION);
    Ok(started_at_millis)
}

/// Does the second half of work, records the interval, and returns the token.
fn leave_critical_section(
    context: &mut Context,
    section: &str,
    started_at_millis: i64,
) -> HandlerResult<()> {
    thread::sleep(CRITICAL_SECTION_STEP_DURATION);
    CRITICAL_SECTION_FINISHED.publish(
        context,
        TaskInterval {
            task: section.to_string(),
            started_at_millis,
            finished_at_millis: current_unix_millis()?,
        },
    )?;
    CRITICAL_SECTION_TOKEN.publish(context, "token".to_string())
}

static AUDIT_LOCK: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new("auditLock"));
static AUDIT_FINISHED: LazyLock<Channel<TaskInterval>> =
    LazyLock::new(|| Channel::new("auditFinished"));

const AUDIT_COUNT: usize = 3;

/// WCP-40 Interleaved Routing: every audit holds the same Execute lock, so
/// they run one at a time in any order.
#[derive(Default)]
pub struct InterleavedRoutingFlow {
    start_audits: StartAudits,
    audit_payroll: AuditPayroll,
    audit_expenses: AuditExpenses,
    audit_taxes: AuditTaxes,
    complete_audits: CompleteAudits,
}

impl Flow for InterleavedRoutingFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_audits)
            .and(&self.audit_payroll)
            .and(&self.audit_expenses)
            .and(&self.audit_taxes)
            .and(&self.complete_audits)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&AUDIT_LOCK)
            .channel(&AUDIT_FINISHED)
    }
}

#[derive(Default)]
struct StartAudits;

impl Step for StartAudits {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&AuditPayroll, ()),
            StepMovement::to(&AuditExpenses, ()),
            StepMovement::to(&AuditTaxes, ()),
            StepMovement::to(&CompleteAudits, AUDIT_COUNT),
        ]))
    }
}

#[derive(Default)]
struct AuditPayroll;

impl Step for AuditPayroll {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(AUDIT_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        run_interleaved_task(context, &AUDIT_FINISHED, self.step_type())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AuditExpenses;

impl Step for AuditExpenses {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(AUDIT_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        run_interleaved_task(context, &AUDIT_FINISHED, self.step_type())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct AuditTaxes;

impl Step for AuditTaxes {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(AUDIT_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        run_interleaved_task(context, &AUDIT_FINISHED, self.step_type())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CompleteAudits;

impl Step for CompleteAudits {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, audit_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(AUDIT_FINISHED.for_n(audit_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _audit_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let intervals = AUDIT_FINISHED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(intervals))
    }
}

/// Works while holding the caller's Execute lock and reports the interval.
fn run_interleaved_task(
    context: &mut Context,
    finished: &Channel<TaskInterval>,
    task: &str,
) -> HandlerResult<()> {
    let started_at_millis = current_unix_millis()?;
    thread::sleep(INTERLEAVED_TASK_DURATION);
    finished.publish(
        context,
        TaskInterval {
            task: task.to_string(),
            started_at_millis,
            finished_at_millis: current_unix_millis()?,
        },
    )
}
