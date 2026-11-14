// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Counts of hot standby restarts, takeover retries, and WARN and ERROR message patterns over
//! every process log of a soak.

use std::collections::BTreeMap;
use std::fmt;

/// The engine's line for each restart; the server's status line repeats only a changed error.
const STANDBY_RESTART_TARGET: &str = "dexzd_engine::hot_standby";
const STANDBY_RESTART_MESSAGE: &str = "the hot standby restarts after an error";
const TAKEOVER_RETRY_MESSAGE: &str =
    "the takeover failed before the node lease; following and retrying";
const LEVELS: [&str; 2] = ["WARN", "ERROR"];
/// Words this long with a digit are IDs, keys, or hashes.
const MIN_ID_LENGTH: usize = 16;
const MAX_PATTERN_CHARS: usize = 200;

/// Sums over the log lines of every process that the soak scanned.
#[derive(Clone, Debug, Default)]
pub(crate) struct LogTally {
    pub(crate) standby_restarts: usize,
    pub(crate) takeover_retries: usize,
    /// WARN and ERROR lines by normalized pattern.
    patterns: BTreeMap<String, usize>,
}

impl LogTally {
    /// Adds every complete line of `text`, a part of one process log.
    pub(crate) fn add_lines(&mut self, text: &str) {
        for line in text.lines() {
            let Some(pattern) = warning_pattern(line) else {
                continue;
            };
            if line.contains(STANDBY_RESTART_MESSAGE) && line.contains(STANDBY_RESTART_TARGET) {
                self.standby_restarts += 1;
            }
            if line.contains(TAKEOVER_RETRY_MESSAGE) {
                self.takeover_retries += 1;
            }
            *self.patterns.entry(pattern).or_default() += 1;
        }
    }

    pub(crate) fn warning_count(&self) -> usize {
        self.patterns.values().sum()
    }

    /// Returns the `limit` most frequent patterns with their counts, most frequent first.
    pub(crate) fn top_patterns(&self, limit: usize) -> Vec<(usize, &str)> {
        let mut patterns: Vec<(usize, &str)> = self
            .patterns
            .iter()
            .map(|(pattern, count)| (*count, pattern.as_str()))
            .collect();
        patterns.sort_by(|left, right| right.0.cmp(&left.0).then(left.1.cmp(right.1)));
        patterns.truncate(limit);
        patterns
    }

    pub(crate) fn distinct_pattern_count(&self) -> usize {
        self.patterns.len()
    }
}

impl fmt::Display for LogTally {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{} hot standby restarts, {} takeover retries, {} WARN/ERROR lines in {} patterns",
            self.standby_restarts,
            self.takeover_retries,
            self.warning_count(),
            self.distinct_pattern_count()
        )
    }
}

/// Returns the normalized pattern of a WARN or ERROR line, without its timestamp.
fn warning_pattern(line: &str) -> Option<String> {
    let rest = line.trim_start();
    let rest = match rest.split_once(char::is_whitespace) {
        Some((first, after))
            if first.contains('T') && first.starts_with(|c: char| c.is_ascii_digit()) =>
        {
            after.trim_start()
        }
        _ => rest,
    };
    let level = rest.split_whitespace().next()?;
    if !LEVELS.contains(&level) {
        return None;
    }
    Some(replace_numbers_and_ids(rest))
}

/// Replaces long words that hold a digit with `<id>` and other digit runs with `N`.
fn replace_numbers_and_ids(text: &str) -> String {
    let mut pattern = String::new();
    let mut word = String::new();
    for character in text.chars() {
        if character.is_ascii_alphanumeric() || character == '-' || character == '_' {
            word.push(character);
        } else {
            push_word(&mut word, &mut pattern);
            pattern.push(character);
        }
        if pattern.chars().count() >= MAX_PATTERN_CHARS {
            break;
        }
    }
    push_word(&mut word, &mut pattern);
    pattern.chars().take(MAX_PATTERN_CHARS).collect()
}

/// Moves `word` into `pattern`, as `<id>` or with each digit run as `N`.
fn push_word(word: &mut String, pattern: &mut String) {
    if word.chars().count() >= MIN_ID_LENGTH && word.chars().any(|c| c.is_ascii_digit()) {
        pattern.push_str("<id>");
    } else {
        let mut is_in_number = false;
        for character in word.chars() {
            if character.is_ascii_digit() {
                if !is_in_number {
                    pattern.push('N');
                }
                is_in_number = true;
            } else {
                pattern.push(character);
                is_in_number = false;
            }
        }
    }
    word.clear();
}
