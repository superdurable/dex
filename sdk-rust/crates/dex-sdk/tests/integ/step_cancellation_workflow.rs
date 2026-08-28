// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::Duration;

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList, StepMovement, StepOptions, Timer, Wait, WaitForFailurePolicy,
};

static LATE_WRITE: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("rust-cancellation-late-write"));

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum CancellationScenario {
    HeartbeatExecute,
    HeartbeatWaitFor,
    LocalExecute,
    LocalTimeoutFallback,
    NoHeartbeat,
    GlobalSelector,
    SiblingSelector,
}

impl CancellationScenario {
    pub(crate) const ALL: [Self; 7] = [
        Self::HeartbeatExecute,
        Self::HeartbeatWaitFor,
        Self::LocalExecute,
        Self::LocalTimeoutFallback,
        Self::NoHeartbeat,
        Self::GlobalSelector,
        Self::SiblingSelector,
    ];

    pub(crate) fn name(self) -> &'static str {
        match self {
            Self::HeartbeatExecute => "heartbeat-execute",
            Self::HeartbeatWaitFor => "heartbeat-wait-for",
            Self::LocalExecute => "local-execute",
            Self::LocalTimeoutFallback => "local-timeout-fallback",
            Self::NoHeartbeat => "no-heartbeat",
            Self::GlobalSelector => "global-selector",
            Self::SiblingSelector => "sibling-selector",
        }
    }
}

#[derive(Default)]
pub(crate) struct Event {
    ready: Mutex<bool>,
    condition: Condvar,
}

impl Event {
    pub(crate) fn set(&self) {
        *self.ready.lock().expect("event lock") = true;
        self.condition.notify_all();
    }

    pub(crate) fn wait(&self, timeout: Duration) -> bool {
        let ready = self.ready.lock().expect("event lock");
        let (ready, _) = self
            .condition
            .wait_timeout_while(ready, timeout, |ready| !*ready)
            .expect("event wait");
        *ready
    }

    pub(crate) fn is_set(&self) -> bool {
        *self.ready.lock().expect("event lock")
    }
}

pub(crate) struct CancellationState {
    pub(crate) scenario: CancellationScenario,
    pub(crate) blocking_started: Event,
    pub(crate) cancellation_observed: Event,
    pub(crate) late_handler_returned: Event,
    pub(crate) selector_waits_registered: Event,
    pub(crate) handler_canceled: AtomicBool,
    pub(crate) context_reported_cancellation: AtomicBool,
    pub(crate) recovery_ran: AtomicBool,
    pub(crate) first_selector_executed: AtomicBool,
    pub(crate) second_selector_executed: AtomicBool,
    pub(crate) blocking_invocations: AtomicU32,
    selector_wait_count: AtomicU32,
}

impl CancellationState {
    pub(crate) fn new(scenario: CancellationScenario) -> Arc<Self> {
        Arc::new(Self {
            scenario,
            blocking_started: Event::default(),
            cancellation_observed: Event::default(),
            late_handler_returned: Event::default(),
            selector_waits_registered: Event::default(),
            handler_canceled: AtomicBool::new(false),
            context_reported_cancellation: AtomicBool::new(false),
            recovery_ran: AtomicBool::new(false),
            first_selector_executed: AtomicBool::new(false),
            second_selector_executed: AtomicBool::new(false),
            blocking_invocations: AtomicU32::new(0),
            selector_wait_count: AtomicU32::new(0),
        })
    }
}

pub(crate) struct StepCancellationWorkflow {
    pub(crate) late_write: Attribute<String>,
    start: CancellationStart,
    blocking_execute: CancellationBlockingExecute,
    blocking_wait_for: CancellationBlockingWaitFor,
    winner: CancellationWinner,
    recovery: CancellationRecovery,
    final_step: CancellationFinal,
    first_parent: CancellationFirstParent,
    second_parent: CancellationSecondParent,
    selector_winner: CancellationSelectorWinner,
    selector_waiting: CancellationSelectorWaiting,
}

impl StepCancellationWorkflow {
    pub(crate) fn new(state: Arc<CancellationState>) -> Self {
        let late_write = LATE_WRITE.clone();
        Self {
            start: CancellationStart(Arc::clone(&state)),
            blocking_execute: CancellationBlockingExecute {
                state: Arc::clone(&state),
                late_write: late_write.clone(),
            },
            blocking_wait_for: CancellationBlockingWaitFor(Arc::clone(&state)),
            winner: CancellationWinner(Arc::clone(&state)),
            recovery: CancellationRecovery(Arc::clone(&state)),
            final_step: CancellationFinal,
            first_parent: CancellationFirstParent(Arc::clone(&state)),
            second_parent: CancellationSecondParent(Arc::clone(&state)),
            selector_winner: CancellationSelectorWinner(Arc::clone(&state)),
            selector_waiting: CancellationSelectorWaiting(Arc::clone(&state)),
            late_write,
        }
    }
}

impl Flow for StepCancellationWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.blocking_execute)
            .and(&self.blocking_wait_for)
            .and(&self.winner)
            .and(&self.recovery)
            .and(&self.final_step)
            .and(&self.first_parent)
            .and(&self.second_parent)
            .and(&self.selector_winner)
            .and(&self.selector_waiting)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&self.late_write)
    }
}

pub(crate) struct CancellationStart(Arc<CancellationState>);

impl Step for CancellationStart {
    type Input = String;

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        let state = Arc::clone(&self.0);
        let decision = match state.scenario {
            CancellationScenario::HeartbeatWaitFor => StepDecision::go_to_many([
                StepMovement::to(&CancellationBlockingWaitFor(Arc::clone(&state)), ()),
                StepMovement::to(&CancellationWinner(state), ()),
            ]),
            CancellationScenario::GlobalSelector | CancellationScenario::SiblingSelector => {
                StepDecision::go_to_many([
                    StepMovement::to(&CancellationFirstParent(Arc::clone(&state)), ()),
                    StepMovement::to(&CancellationSecondParent(state), ()),
                ])
            }
            _ => StepDecision::go_to_many([
                StepMovement::to(
                    &CancellationBlockingExecute {
                        state: Arc::clone(&state),
                        late_write: LATE_WRITE.clone(),
                    },
                    (),
                ),
                StepMovement::to(&CancellationWinner(state), ()),
            ]),
        };
        Ok(decision)
    }
}

pub(crate) struct CancellationBlockingExecute {
    pub(crate) state: Arc<CancellationState>,
    pub(crate) late_write: Attribute<String>,
}

impl Step for CancellationBlockingExecute {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        self.state
            .blocking_invocations
            .fetch_add(1, Ordering::SeqCst);
        self.state.blocking_started.set();
        if self.state.scenario == CancellationScenario::NoHeartbeat {
            thread::sleep(Duration::from_secs(7));
        } else {
            context.wait_for_cancellation();
            self.state.handler_canceled.store(true, Ordering::SeqCst);
            self.state
                .context_reported_cancellation
                .store(context.is_cancelled(), Ordering::SeqCst);
            self.state.cancellation_observed.set();
        }
        self.late_write.set(context, "late".to_string())?;
        self.state.late_handler_returned.set();
        Ok(StepDecision::go_to(
            &CancellationRecovery(Arc::clone(&self.state)),
            (),
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        let mut options = StepOptions::new()
            .execute_method_timeout(Duration::from_secs(15))
            .on_execute_failure_proceed_to(&CancellationRecovery(Arc::clone(&self.state)));
        if matches!(
            self.state.scenario,
            CancellationScenario::HeartbeatExecute | CancellationScenario::LocalTimeoutFallback
        ) {
            options = options.heartbeat_timeout(Duration::from_secs(2));
        }
        if matches!(
            self.state.scenario,
            CancellationScenario::LocalExecute | CancellationScenario::LocalTimeoutFallback
        ) {
            options = options.execute_durability(dex_sdk::StepDurability::Async);
        } else {
            options = options.execute_durability(dex_sdk::StepDurability::Sync);
        }
        options
    }
}

pub(crate) struct CancellationBlockingWaitFor(pub(crate) Arc<CancellationState>);

impl Step for CancellationBlockingWaitFor {
    type Input = ();

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        self.0.blocking_invocations.fetch_add(1, Ordering::SeqCst);
        self.0.blocking_started.set();
        context.wait_for_cancellation();
        self.0.handler_canceled.store(true, Ordering::SeqCst);
        self.0
            .context_reported_cancellation
            .store(context.is_cancelled(), Ordering::SeqCst);
        self.0.cancellation_observed.set();
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        self.0.recovery_ran.store(true, Ordering::SeqCst);
        Ok(StepDecision::force_fail(
            "canceled wait_for execution continued",
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_method_timeout(Duration::from_secs(15))
            .heartbeat_timeout(Duration::from_secs(2))
            .wait_for_failure(WaitForFailurePolicy::Proceed)
            .wait_for_durability(dex_sdk::StepDurability::Sync)
    }
}

struct CancellationWinner(Arc<CancellationState>);

impl Step for CancellationWinner {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        if self.0.scenario == CancellationScenario::LocalExecute {
            return Ok(Wait::skip_immediately());
        }
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(3))))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        if self.0.scenario == CancellationScenario::LocalExecute {
            if !self.0.blocking_started.wait(Duration::from_secs(10)) {
                return Err(HandlerError::new(
                    "StepCancellationFailure",
                    "local loser did not start",
                ));
            }
            thread::sleep(Duration::from_secs(1));
        }
        let decision = StepDecision::go_to(&CancellationFinal, self.0.scenario.name().to_string());
        if self.0.scenario == CancellationScenario::HeartbeatWaitFor {
            return Ok(decision.cancel_step(&CancellationBlockingWaitFor(Arc::clone(&self.0))));
        }
        Ok(decision.cancel_step(&CancellationBlockingExecute {
            state: Arc::clone(&self.0),
            late_write: LATE_WRITE.clone(),
        }))
    }
}

struct CancellationRecovery(Arc<CancellationState>);

impl Step for CancellationRecovery {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        self.0.recovery_ran.store(true, Ordering::SeqCst);
        Ok(StepDecision::force_fail(
            "canceled execution reached recovery",
        ))
    }
}

struct CancellationFinal;

impl Step for CancellationFinal {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}

struct CancellationFirstParent(Arc<CancellationState>);

impl Step for CancellationFirstParent {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&CancellationSelectorWinner(Arc::clone(&self.0)), ()),
            StepMovement::to(
                &CancellationSelectorWaiting(Arc::clone(&self.0)),
                "first".to_string(),
            ),
        ]))
    }
}

struct CancellationSecondParent(Arc<CancellationState>);

impl Step for CancellationSecondParent {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &CancellationSelectorWaiting(Arc::clone(&self.0)),
            "second".to_string(),
        ))
    }
}

struct CancellationSelectorWinner(Arc<CancellationState>);

impl Step for CancellationSelectorWinner {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        if !self
            .0
            .selector_waits_registered
            .wait(Duration::from_secs(10))
        {
            return Err(HandlerError::new(
                "StepCancellationFailure",
                "selector Steps did not reach waiting",
            ));
        }
        let decision = StepDecision::go_to(&CancellationFinal, self.0.scenario.name().to_string());
        if self.0.scenario == CancellationScenario::GlobalSelector {
            return Ok(decision.cancel_step(&CancellationSelectorWaiting(Arc::clone(&self.0))));
        }
        Ok(decision.cancel_sibling_step(&CancellationSelectorWaiting(Arc::clone(&self.0))))
    }
}

struct CancellationSelectorWaiting(Arc<CancellationState>);

impl Step for CancellationSelectorWaiting {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, input: String) -> HandlerResult<Wait> {
        if self.0.selector_wait_count.fetch_add(1, Ordering::SeqCst) == 1 {
            self.0.selector_waits_registered.set();
        }
        let duration =
            if input == "first" || self.0.scenario == CancellationScenario::GlobalSelector {
                30
            } else {
                2
            };
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            duration,
        ))))
    }

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        if input == "first" {
            self.0.first_selector_executed.store(true, Ordering::SeqCst);
        } else {
            self.0
                .second_selector_executed
                .store(true, Ordering::SeqCst);
        }
        Ok(StepDecision::dead_end())
    }
}
