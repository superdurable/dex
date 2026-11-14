// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

const RESERVED_TASK_QUEUE_PREFIX: &str = "dex.";
const DEFAULT_TASK_QUEUE_ALIAS: &str = "dex.default";
const MAXIMUM_TASK_QUEUE_NAME_LENGTH: usize = 63;

/// Returns `Ok(())` when a pull Worker may register `task_queue`, including the empty default
/// task queue, and an error message when the name is invalid or reserved.
pub(crate) fn validate_pull_worker_task_queue(task_queue: &str) -> Result<(), String> {
    if task_queue.is_empty() {
        return Ok(());
    }
    if task_queue.starts_with(RESERVED_TASK_QUEUE_PREFIX) {
        return Err(format!(
            "task queue {task_queue:?} is invalid: names starting with \"dex.\" are reserved"
        ));
    }
    validate_task_queue_characters(task_queue)
}

/// Returns `Ok(())` when a Flow's Worker target may carry `task_queue`: the empty name,
/// `dex.default`, or a valid unreserved name. Returns an error message otherwise.
pub(crate) fn validate_flow_task_queue(task_queue: &str) -> Result<(), String> {
    if task_queue == DEFAULT_TASK_QUEUE_ALIAS {
        return Ok(());
    }
    validate_pull_worker_task_queue(task_queue)
}

fn validate_task_queue_characters(task_queue: &str) -> Result<(), String> {
    let is_valid = task_queue.len() <= MAXIMUM_TASK_QUEUE_NAME_LENGTH
        && task_queue.chars().all(|character| {
            character.is_ascii_alphanumeric() || matches!(character, '.' | '_' | '-')
        });
    if is_valid {
        Ok(())
    } else {
        Err(format!(
            "task queue {task_queue:?} is invalid: use 1-63 ASCII letters, digits, dots, \
             underscores, or hyphens"
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::{validate_flow_task_queue, validate_pull_worker_task_queue};

    #[test]
    fn worker_task_queues_accept_valid_names_and_reject_reserved_ones() {
        for valid in ["", "blue", "Blue-2", "a.b_c-d", &"q".repeat(63)] {
            validate_pull_worker_task_queue(valid).expect("valid Worker task queue");
        }
        for invalid in [
            "dex.default",
            "dex.other",
            "has space",
            "slash/name",
            "\u{e9}",
            &"q".repeat(64),
        ] {
            assert!(
                validate_pull_worker_task_queue(invalid).is_err(),
                "{invalid:?} must be rejected"
            );
        }
        let reserved = validate_pull_worker_task_queue("dex.default").expect_err("reserved");
        assert!(reserved.contains("reserved"), "{reserved}");
    }

    #[test]
    fn flow_task_queues_also_accept_the_default_alias() {
        for valid in ["", "dex.default", "blue"] {
            validate_flow_task_queue(valid).expect("valid Flow task queue");
        }
        for invalid in ["dex.other", "dex.", "bad name", &"q".repeat(64)] {
            assert!(
                validate_flow_task_queue(invalid).is_err(),
                "{invalid:?} must be rejected"
            );
        }
    }
}
