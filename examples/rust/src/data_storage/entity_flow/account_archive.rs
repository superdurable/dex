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
#[derive(Default)]
pub struct FakeAccountArchive {
    state: Mutex<AccountArchiveState>,
}

#[derive(Default)]
struct AccountArchiveState {
    unavailable_flow_ids: HashSet<String>,
    archive_counts: HashMap<String, usize>,
}

impl FakeAccountArchive {
    pub fn archive_account(&self, flow_id: &str) -> Result<(), String> {
        let mut state = self.lock_state();
        if state.unavailable_flow_ids.contains(flow_id) {
            return Err("account archive is unavailable".to_owned());
        }
        *state.archive_counts.entry(flow_id.to_owned()).or_default() += 1;
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

    pub fn archive_count(&self, flow_id: &str) -> usize {
        self.lock_state()
            .archive_counts
            .get(flow_id)
            .copied()
            .unwrap_or_default()
    }

    fn lock_state(&self) -> std::sync::MutexGuard<'_, AccountArchiveState> {
        self.state
            .lock()
            .expect("account archive lock is never held across a panic")
    }
}
