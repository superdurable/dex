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

use std::collections::{HashMap, HashSet};
use std::sync::Mutex;

/// In-memory stand-in for the system that keeps records of closed accounts.
/// Archiving is idempotent per account, as a unique insert keyed by the Flow ID would be.
#[derive(Default)]
pub struct FakeAccountArchive {
    state: Mutex<AccountArchiveState>,
}

#[derive(Default)]
struct AccountArchiveState {
    unavailable_flow_ids: HashSet<String>,
    lost_response_flow_ids: HashSet<String>,
    archive_records: HashMap<String, String>,
    attempt_step_execution_ids: HashMap<String, Vec<String>>,
}

impl FakeAccountArchive {
    pub fn archive_account(&self, flow_id: &str, step_execution_id: &str) -> Result<(), String> {
        let mut state = self.lock_state();
        state
            .attempt_step_execution_ids
            .entry(flow_id.to_owned())
            .or_default()
            .push(step_execution_id.to_owned());
        if state.archive_records.contains_key(flow_id) {
            return Ok(());
        }
        if state.unavailable_flow_ids.contains(flow_id) {
            return Err("account archive is unavailable".to_owned());
        }
        state
            .archive_records
            .insert(flow_id.to_owned(), step_execution_id.to_owned());
        if state.lost_response_flow_ids.remove(flow_id) {
            return Err("account archive response was lost".to_owned());
        }
        Ok(())
    }

    pub fn make_unavailable(&self, flow_id: &str) {
        self.lock_state()
            .unavailable_flow_ids
            .insert(flow_id.to_owned());
    }

    pub fn make_available(&self, flow_id: &str) {
        self.lock_state().unavailable_flow_ids.remove(flow_id);
    }

    pub fn lose_next_response(&self, flow_id: &str) {
        self.lock_state()
            .lost_response_flow_ids
            .insert(flow_id.to_owned());
    }

    pub fn archive_record_count(&self, flow_id: &str) -> usize {
        usize::from(self.lock_state().archive_records.contains_key(flow_id))
    }

    pub fn attempt_step_execution_ids(&self, flow_id: &str) -> Vec<String> {
        self.lock_state()
            .attempt_step_execution_ids
            .get(flow_id)
            .cloned()
            .unwrap_or_default()
    }

    fn lock_state(&self) -> std::sync::MutexGuard<'_, AccountArchiveState> {
        self.state
            .lock()
            .expect("account archive lock is never held across a panic")
    }
}
