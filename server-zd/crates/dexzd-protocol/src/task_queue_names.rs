// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Task queue name rules shared by request validation, Worker registration, and apply.

/// Longest task queue name, in bytes.
pub const MAX_TASK_QUEUE_NAME_BYTES: usize = 63;

/// Prefix of names that Dex reserves.
pub const RESERVED_TASK_QUEUE_PREFIX: &str = "dex.";

/// The reserved name that selects the default task queue in an override.
pub const DEFAULT_TASK_QUEUE_ALIAS: &str = "dex.default";

/// Returns why `name` is not a task queue name that a Worker may register, or `None`.
///
/// The empty name is the default task queue and is valid. Every name starting with
/// [`RESERVED_TASK_QUEUE_PREFIX`] is invalid.
pub fn registered_task_queue_name_error(name: &str) -> Option<&'static str> {
    if name.is_empty() {
        return None;
    }
    if name.starts_with(RESERVED_TASK_QUEUE_PREFIX) {
        return Some("task queue names starting with \"dex.\" are reserved");
    }
    task_queue_characters_error(name)
}

/// Returns why `name` is not a task queue name that a WorkerTarget may carry, or `None`.
///
/// It accepts what [`registered_task_queue_name_error`] accepts, plus
/// [`DEFAULT_TASK_QUEUE_ALIAS`].
pub fn target_task_queue_name_error(name: &str) -> Option<&'static str> {
    if name == DEFAULT_TASK_QUEUE_ALIAS {
        return None;
    }
    registered_task_queue_name_error(name)
}

/// Returns the task queue that an override of `base_task_queue` with `override_task_queue`
/// selects.
///
/// Returns `base_task_queue` for an empty override, the empty default name for
/// [`DEFAULT_TASK_QUEUE_ALIAS`], and `override_task_queue` otherwise.
pub fn overridden_task_queue(base_task_queue: &str, override_task_queue: &str) -> String {
    match override_task_queue {
        "" => base_task_queue.to_owned(),
        DEFAULT_TASK_QUEUE_ALIAS => String::new(),
        named => named.to_owned(),
    }
}

fn task_queue_characters_error(name: &str) -> Option<&'static str> {
    if name.len() > MAX_TASK_QUEUE_NAME_BYTES {
        return Some("task queue names have at most 63 characters");
    }
    if !name
        .bytes()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
    {
        return Some("task queue names use only A-Z, a-z, 0-9, '.', '_', and '-'");
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn names_follow_the_length_character_and_reserved_prefix_rules() {
        assert_eq!(registered_task_queue_name_error(""), None);
        assert_eq!(registered_task_queue_name_error("env-1.A_b"), None);
        assert_eq!(
            registered_task_queue_name_error(&"q".repeat(MAX_TASK_QUEUE_NAME_BYTES)),
            None
        );
        assert!(registered_task_queue_name_error(&"q".repeat(64)).is_some());
        assert!(registered_task_queue_name_error("has space").is_some());
        assert!(registered_task_queue_name_error("ü").is_some());
        assert!(registered_task_queue_name_error("dex.default").is_some());
        assert!(registered_task_queue_name_error("dex.other").is_some());
        assert_eq!(target_task_queue_name_error("dex.default"), None);
        assert!(target_task_queue_name_error("dex.other").is_some());
    }

    #[test]
    fn an_override_keeps_the_base_unless_it_names_a_queue_or_the_default() {
        assert_eq!(overridden_task_queue("parent", ""), "parent");
        assert_eq!(
            overridden_task_queue("parent", DEFAULT_TASK_QUEUE_ALIAS),
            ""
        );
        assert_eq!(overridden_task_queue("parent", "child"), "child");
        assert_eq!(overridden_task_queue("", ""), "");
    }
}
