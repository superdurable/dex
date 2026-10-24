// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

impl PullStream {
    pub(super) async fn run(mut self) {
        let _registration = StreamRegistration {
            shared: Arc::clone(&self.shared),
            stream_id: self.stream_id,
        };
        let mut backoff = Duration::from_millis(100);
        let mut has_immediate_auth_reconnect = false;
        loop {
            if self.stop.borrow().phase != 0 {
                break;
            }
            let (was_registered, is_reconnecting, result) = self.poll().await;
            if self.stop.borrow().phase != 0 || self.shared.is_retiring(self.stream_id) {
                self.record_stream_result(None);
                break;
            }
            if was_registered {
                has_immediate_auth_reconnect = false;
            }
            if result
                .as_ref()
                .is_err_and(|status| status.code() == Code::Unauthenticated)
                && !has_immediate_auth_reconnect
                && self.prepare_immediate_auth_reconnect().await
            {
                self.record_stream_result(None);
                has_immediate_auth_reconnect = true;
                continue;
            }
            self.record_stream_result(result.as_ref().err());
            has_immediate_auth_reconnect = false;
            if was_registered || is_reconnecting {
                backoff = Duration::from_millis(100);
            }
            let has_permanent_error = result
                .as_ref()
                .is_err_and(|status| is_permanent(status.code()));
            match &result {
                Err(status) if status.code() == Code::Unimplemented => {
                    let mut state = self.shared.state.lock().expect("pull state lock");
                    if !state.logged_unsupported {
                        state.logged_unsupported = true;
                        tracing::error!(%status, "pull Worker polling is unsupported by this Dex server");
                    }
                    backoff = Duration::from_secs(60);
                }
                Err(status) if status.code() == Code::Unauthenticated => {
                    tracing::error!(queue = %self.queue, "Worker task stream authentication rejected");
                    backoff = Duration::from_secs(60);
                }
                Err(status) if is_permanent(status.code()) => {
                    tracing::error!(%status, queue = %self.queue, "Worker task stream rejected");
                    backoff = Duration::from_secs(60);
                }
                Err(status) => {
                    tracing::warn!(%status, queue = %self.queue, "Worker task stream disconnected");
                }
                Ok(()) => {}
            }
            let jitter = Duration::from_millis(
                (uuid::Uuid::new_v4().as_u128() as u64) % (backoff.as_millis() as u64 / 2).max(1),
            );
            tokio::select! {
                () = sleep(backoff + jitter) => {}
                change = self.stop.changed() => { if change.is_err() || self.stop.borrow().phase != 0 { break; } }
            }
            if !has_permanent_error {
                backoff = (backoff * 2).min(Duration::from_secs(5));
            }
        }
    }

    async fn prepare_immediate_auth_reconnect(&mut self) -> bool {
        let Some(credential) = &self.credential else {
            return false;
        };
        let Some(refresh) = credential.token_after_rejection() else {
            return false;
        };
        tokio::select! {
            result = refresh => result.is_ok(),
            change = self.stop.changed() => {
                if change.is_err() || self.stop.borrow().phase != 0 { return false; }
                false
            }
        }
    }
}
