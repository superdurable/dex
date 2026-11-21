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

//! A fake email service that prints each message and sends one email per key.

use std::fmt::{self, Display, Formatter};
use std::path::PathBuf;

use super::json_ledger::{read_json_ledger, update_json_ledger};

pub const DEFAULT_EMAIL_LEDGER: &str = ".customer-refund/sent-emails.json";

#[derive(Debug)]
pub struct EmailLedgerError {
    path: PathBuf,
    detail: String,
}

impl Display for EmailLedgerError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "read or write the email ledger {}: {}",
            self.path.display(),
            self.detail
        )
    }
}

impl std::error::Error for EmailLedgerError {}

/// Keeps sent keys in a JSON file, so a restarted Worker sees earlier emails.
pub struct FakeEmailService {
    ledger_path: PathBuf,
}

impl FakeEmailService {
    pub fn new(ledger_path: impl Into<PathBuf>) -> Self {
        Self {
            ledger_path: ledger_path.into(),
        }
    }

    /// Returns `true` when this call sent the email, or `false` when an earlier call sent it.
    ///
    /// The ledger write, which keeps only the key, is the send; the printed line is a log.
    pub fn send(
        &self,
        to: &str,
        body: &str,
        idempotency_key: &str,
    ) -> Result<bool, EmailLedgerError> {
        let is_new = update_json_ledger(&self.ledger_path, |sent: &mut Vec<String>| {
            if sent.iter().any(|key| key == idempotency_key) {
                return (false, false);
            }
            sent.push(idempotency_key.to_string());
            (true, true)
        })
        .map_err(|detail| self.ledger_error(detail))?;
        if is_new {
            println!("Emailed {to}: {body}");
        } else {
            println!("Email to {to} already sent (key {idempotency_key})");
        }
        Ok(is_new)
    }

    pub fn list_sent_keys(&self) -> Result<Vec<String>, EmailLedgerError> {
        read_json_ledger(&self.ledger_path).map_err(|detail| self.ledger_error(detail))
    }

    fn ledger_error(&self, detail: String) -> EmailLedgerError {
        EmailLedgerError {
            path: self.ledger_path.clone(),
            detail,
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Barrier};
    use std::thread;

    use super::FakeEmailService;

    const CONCURRENT_CALLS: usize = 8;

    fn send_concurrently(email: &Arc<FakeEmailService>, key_for: fn(usize) -> String) -> Vec<bool> {
        let barrier = Arc::new(Barrier::new(CONCURRENT_CALLS));
        let calls: Vec<_> = (0..CONCURRENT_CALLS)
            .map(|index| {
                let email = Arc::clone(email);
                let barrier = Arc::clone(&barrier);
                thread::spawn(move || {
                    barrier.wait();
                    email
                        .send("billing@northstar.example", "Hi", &key_for(index))
                        .unwrap()
                })
            })
            .collect();
        calls.into_iter().map(|call| call.join().unwrap()).collect()
    }

    #[test]
    fn concurrent_sends_with_one_key_send_once() {
        let directory = tempfile::tempdir().unwrap();
        let email = Arc::new(FakeEmailService::new(directory.path().join("sent.json")));

        let sent = send_concurrently(&email, |_| "email-same".to_string());

        assert_eq!(sent.iter().filter(|is_new| **is_new).count(), 1);
        assert_eq!(email.list_sent_keys().unwrap(), vec!["email-same"]);
    }

    #[test]
    fn concurrent_sends_with_different_keys_send_each() {
        let directory = tempfile::tempdir().unwrap();
        let email = Arc::new(FakeEmailService::new(directory.path().join("sent.json")));

        let sent = send_concurrently(&email, |index| format!("email-{index}"));

        assert!(sent.iter().all(|is_new| *is_new));
        let mut keys = email.list_sent_keys().unwrap();
        keys.sort();
        let mut expected: Vec<_> = (0..CONCURRENT_CALLS)
            .map(|i| format!("email-{i}"))
            .collect();
        expected.sort();
        assert_eq!(keys, expected);
    }
}
