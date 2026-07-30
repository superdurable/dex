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

use std::sync::{Condvar, Mutex};

use super::format::FileMetadata;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum EntryStatus {
    Pending,
    Ready,
    Evicted,
}

#[derive(Debug)]
struct EntryState {
    status: EntryStatus,
    readers: usize,
}

#[derive(Debug)]
pub(crate) struct DiskEntry {
    pub(crate) metadata: FileMetadata,
    state: Mutex<EntryState>,
    readers_drained: Condvar,
}

impl DiskEntry {
    pub(crate) fn pending(metadata: FileMetadata) -> Self {
        Self {
            metadata,
            state: Mutex::new(EntryState {
                status: EntryStatus::Pending,
                readers: 0,
            }),
            readers_drained: Condvar::new(),
        }
    }

    pub(crate) fn ready(metadata: FileMetadata) -> Self {
        let entry = Self::pending(metadata);
        entry
            .state
            .lock()
            .expect("new disk entry mutex is not poisoned")
            .status = EntryStatus::Ready;
        entry
    }

    pub(crate) fn acquire_read(&self) -> Option<ReadLease<'_>> {
        let mut state = self.state.lock().expect("disk entry mutex is poisoned");
        if state.status != EntryStatus::Ready {
            return None;
        }
        state.readers += 1;
        Some(ReadLease { entry: self })
    }

    pub(crate) fn begin_eviction(&self) -> bool {
        let mut state = self.state.lock().expect("disk entry mutex is poisoned");
        if state.status == EntryStatus::Evicted {
            return false;
        }
        let was_ready = state.status == EntryStatus::Ready;
        state.status = EntryStatus::Evicted;
        while was_ready && state.readers != 0 {
            state = self
                .readers_drained
                .wait(state)
                .expect("disk entry mutex is poisoned");
        }
        was_ready
    }

    pub(crate) fn mark_ready(&self) -> bool {
        let mut state = self.state.lock().expect("disk entry mutex is poisoned");
        if state.status != EntryStatus::Pending {
            return false;
        }
        state.status = EntryStatus::Ready;
        true
    }

    pub(crate) fn restore_ready(&self) {
        self.state
            .lock()
            .expect("disk entry mutex is poisoned")
            .status = EntryStatus::Ready;
    }

    fn release_read(&self) {
        let mut state = self.state.lock().expect("disk entry mutex is poisoned");
        state.readers -= 1;
        if state.readers == 0 {
            self.readers_drained.notify_all();
        }
    }
}

pub(crate) struct ReadLease<'a> {
    entry: &'a DiskEntry,
}

impl Drop for ReadLease<'_> {
    fn drop(&mut self) {
        self.entry.release_read();
    }
}
