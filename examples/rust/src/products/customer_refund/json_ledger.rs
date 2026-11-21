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

//! Reads and changes the JSON files where the fake services remember their keys.

use std::fs::{File, OpenOptions};
use std::path::{Path, PathBuf};

use serde::Serialize;
use serde::de::DeserializeOwned;

/// Returns the ledger's entries, or none when the file does not exist yet.
pub(crate) fn read_json_ledger<T: DeserializeOwned>(path: &Path) -> Result<Vec<T>, String> {
    match std::fs::read_to_string(path) {
        Ok(text) => serde_json::from_str(&text).map_err(|error| error.to_string()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(Vec::new()),
        Err(error) => Err(error.to_string()),
    }
}

/// Applies `change` under a cross-process file lock; rewrites by atomic rename only when it reports a change.
pub(crate) fn update_json_ledger<T, R>(
    path: &Path,
    change: impl FnOnce(&mut Vec<T>) -> (bool, R),
) -> Result<R, String>
where
    T: DeserializeOwned + Serialize,
{
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).map_err(|error| error.to_string())?;
    }
    let lock_file = open_lock_file(path)?;
    lock_file.lock().map_err(|error| error.to_string())?;
    let mut entries = read_json_ledger(path)?;
    let (changed, result) = change(&mut entries);
    if changed {
        replace_json_ledger(path, &entries)?;
    }
    // Dropping the file also releases the lock; unlock first to report a failure.
    lock_file.unlock().map_err(|error| error.to_string())?;
    Ok(result)
}

fn open_lock_file(path: &Path) -> Result<File, String> {
    OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(sibling_path(path, "lock"))
        .map_err(|error| error.to_string())
}

fn replace_json_ledger<T: Serialize>(path: &Path, entries: &[T]) -> Result<(), String> {
    let text = serde_json::to_string_pretty(entries).map_err(|error| error.to_string())?;
    let temporary_path = sibling_path(path, &format!("tmp-{}", std::process::id()));
    std::fs::write(&temporary_path, text).map_err(|error| error.to_string())?;
    std::fs::rename(&temporary_path, path).map_err(|error| error.to_string())
}

fn sibling_path(path: &Path, suffix: &str) -> PathBuf {
    let mut name = path.file_name().unwrap_or_default().to_os_string();
    name.push(".");
    name.push(suffix);
    path.with_file_name(name)
}
