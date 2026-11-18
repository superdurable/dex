// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

//! Flows for the adaptive slot stream tests: Steps whose input chooses how long they hold a slot,
//! and a parked Flow that RPCs target.

use std::collections::HashSet;
use std::sync::{Arc, Condvar, Mutex, mpsc};
use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, Rpc, RpcList, RpcResult, Step, StepDecision,
    StepList, Timer, Wait,
};

pub(crate) const MODE_BLOCK: &str = "block";
pub(crate) const MODE_STUBBORN: &str = "stubborn";
pub(crate) const MODE_QUICK: &str = "quick";
pub(crate) const STUBBORN_RPC_KEY: &str = "rpc";

/// One handler entry: the Flow ID, `step` or `rpc`, and the attempt number.
#[derive(Clone, Debug)]
pub(crate) struct AdaptiveSlotEntry {
    pub(crate) flow_id: String,
    pub(crate) kind: &'static str,
    pub(crate) attempt: u32,
}

/// Records handler entries and releases blocked handlers.
pub(crate) struct AdaptiveSlotHandlers {
    entered: Mutex<mpsc::Sender<AdaptiveSlotEntry>>,
    entries: Mutex<mpsc::Receiver<AdaptiveSlotEntry>>,
    is_released: Mutex<bool>,
    released: Condvar,
    /// Handlers that ignore cancellation, released by Flow ID or by [`STUBBORN_RPC_KEY`].
    stubborn_released: Mutex<HashSet<String>>,
    stubborn_changed: Condvar,
    is_rpc_stubborn: bool,
}

impl AdaptiveSlotHandlers {
    pub(crate) fn new(is_rpc_stubborn: bool) -> Arc<Self> {
        let (entered, entries) = mpsc::channel();
        Arc::new(Self {
            entered: Mutex::new(entered),
            entries: Mutex::new(entries),
            is_released: Mutex::new(false),
            released: Condvar::new(),
            stubborn_released: Mutex::new(HashSet::new()),
            stubborn_changed: Condvar::new(),
            is_rpc_stubborn,
        })
    }

    fn record(&self, context: &Context, kind: &'static str) {
        let entry = AdaptiveSlotEntry {
            flow_id: context.flow_id().to_string(),
            kind,
            attempt: context.attempt(),
        };
        self.entered
            .lock()
            .expect("entry sender lock")
            .send(entry)
            .expect("the test keeps the entry receiver");
    }

    /// Releases every blocked handler; later blocked handlers return at once.
    pub(crate) fn release(&self) {
        *self.is_released.lock().expect("release lock") = true;
        self.released.notify_all();
    }

    /// Waits for release or cancellation; returns false when the attempt was cancelled.
    fn wait_for_release(&self, context: &Context) -> bool {
        let mut is_released = self.is_released.lock().expect("release lock");
        while !*is_released {
            if context.is_cancelled() {
                return false;
            }
            is_released = self
                .released
                .wait_timeout(is_released, Duration::from_millis(20))
                .expect("release lock")
                .0;
        }
        true
    }

    pub(crate) fn release_stubborn(&self, keys: &[&str]) {
        let mut released = self.stubborn_released.lock().expect("stubborn lock");
        for key in keys {
            released.insert((*key).to_string());
        }
        self.stubborn_changed.notify_all();
    }

    /// Lets every handler that ignores cancellation return, so a failed test can stop its Worker.
    pub(crate) fn release_all_stubborn(&self) {
        self.release_stubborn(&["*"]);
    }

    fn wait_for_stubborn_release(&self, key: &str) {
        let mut released = self.stubborn_released.lock().expect("stubborn lock");
        while !released.contains(key) && !released.contains("*") {
            released = self.stubborn_changed.wait(released).expect("stubborn lock");
        }
    }

    /// Waits for `count` handler entries; panics after 60 seconds.
    pub(crate) fn await_entries(&self, count: usize) -> Vec<AdaptiveSlotEntry> {
        let entries = self.entries.lock().expect("entry receiver lock");
        let deadline = std::time::Instant::now() + Duration::from_secs(60);
        let mut received = Vec::with_capacity(count);
        while received.len() < count {
            let remaining = deadline.saturating_duration_since(std::time::Instant::now());
            match entries.recv_timeout(remaining) {
                Ok(entry) => received.push(entry),
                Err(error) => panic!(
                    "only {} of {count} handlers started: {error}",
                    received.len()
                ),
            }
        }
        received
    }

    /// Returns the entries recorded so far.
    pub(crate) fn drain_entries(&self) -> Vec<AdaptiveSlotEntry> {
        self.entries
            .lock()
            .expect("entry receiver lock")
            .try_iter()
            .collect()
    }
}

/// Runs one Step whose input chooses how long it holds its slot.
pub(crate) struct AdaptiveSlotFlow {
    step: AdaptiveSlotStep,
}

impl AdaptiveSlotFlow {
    pub(crate) const GET_SLOT_STATUS: Rpc<(), String> = Rpc::new("get_slot_status");

    pub(crate) fn new(handlers: &Arc<AdaptiveSlotHandlers>) -> Self {
        Self {
            step: AdaptiveSlotStep {
                handlers: Arc::clone(handlers),
            },
        }
    }

    fn get_slot_status(&self, _context: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new("ok".to_string()))
    }
}

impl Flow for AdaptiveSlotFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.step)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function_without_input(Self::GET_SLOT_STATUS, Self::get_slot_status)
    }
}

struct AdaptiveSlotStep {
    handlers: Arc<AdaptiveSlotHandlers>,
}

impl Step for AdaptiveSlotStep {
    type Input = String;

    fn execute(&self, context: &mut Context, mode: String) -> HandlerResult<StepDecision> {
        self.handlers.record(context, "step");
        match mode.as_str() {
            MODE_BLOCK => {
                if !self.handlers.wait_for_release(context) {
                    return Err(HandlerError::new("Cancelled", "the attempt was cancelled"));
                }
            }
            // Only the first attempt ignores cancellation, so the retry after a restart completes.
            MODE_STUBBORN if context.attempt() == 1 => {
                self.handlers.wait_for_stubborn_release(context.flow_id());
            }
            _ => {}
        }
        Ok(StepDecision::graceful_complete(mode))
    }
}

/// Stays running without a slot, so RPCs can target it.
pub(crate) struct AdaptiveSlotParkedFlow {
    step: AdaptiveSlotParkedStep,
    handlers: Arc<AdaptiveSlotHandlers>,
}

impl AdaptiveSlotParkedFlow {
    pub(crate) const BLOCK_UNTIL_RELEASED: Rpc<(), String> = Rpc::new("block_until_released");
    pub(crate) const GET_SLOT_STATUS: Rpc<(), String> = Rpc::new("get_parked_slot_status");

    pub(crate) fn new(handlers: &Arc<AdaptiveSlotHandlers>) -> Self {
        Self {
            step: AdaptiveSlotParkedStep,
            handlers: Arc::clone(handlers),
        }
    }

    fn block_until_released(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        self.handlers.record(context, "rpc");
        if self.handlers.is_rpc_stubborn {
            self.handlers.wait_for_stubborn_release(STUBBORN_RPC_KEY);
            return Ok(RpcResult::new("released".to_string()));
        }
        if !self.handlers.wait_for_release(context) {
            return Err(HandlerError::new("Cancelled", "the RPC was cancelled"));
        }
        Ok(RpcResult::new("released".to_string()))
    }

    fn get_slot_status(&self, _context: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new("ok".to_string()))
    }
}

impl Flow for AdaptiveSlotParkedFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.step)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(Self::BLOCK_UNTIL_RELEASED, Self::block_until_released)
            .function_without_input(Self::GET_SLOT_STATUS, Self::get_slot_status)
    }
}

struct AdaptiveSlotParkedStep;

impl Step for AdaptiveSlotParkedStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(3600))))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("parked".to_string()))
    }
}
