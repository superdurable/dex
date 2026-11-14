// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The `stale_listing` fault: an eventually consistent ListObjectsV2, as Tencent COS serves it.

use std::collections::BTreeMap;
use std::future::Future;
use std::time::{Duration, Instant};

use bytes::Bytes;
use http::Response;
use http_body_util::BodyExt;

use super::Controls;
use crate::s3_error::S3Error;
use crate::server::{S3Response, full_body};

/// Successful listing responses by bucket and exact prefix.
#[derive(Default)]
pub(crate) struct StaleListingCache {
    listings: BTreeMap<(String, String), CachedListing>,
    /// Counts clears, so a listing that started before one does not refill the cache.
    generation: u64,
}

struct CachedListing {
    expires_at: Instant,
    parts: http::response::Parts,
    body: Bytes,
}

impl StaleListingCache {
    pub(crate) fn clear(&mut self) {
        self.listings.clear();
        self.generation = self.generation.wrapping_add(1);
    }
}

impl Controls {
    /// Returns the cached response of the first successful listing of `bucket` and `prefix`
    /// to finish, while it is younger than `ttl`, whatever the query asks. Concurrent listings
    /// that finish later return that response too. Otherwise returns the result of `list`, and
    /// caches its response when it succeeds. Hits do not extend the lifetime.
    pub(crate) async fn serve_stale_listing(
        &self,
        bucket: &str,
        prefix: &str,
        ttl: Duration,
        list: impl Future<Output = Result<S3Response, S3Error>>,
    ) -> Result<S3Response, S3Error> {
        let cache_key = (bucket.to_owned(), prefix.to_owned());
        let generation = {
            let mut state = self.state.lock().expect("control operations cannot panic");
            if let Some(response) = state.stale_listings.live_response(&cache_key) {
                return Ok(response);
            }
            state.stale_listings.generation
        };
        let response = list.await?;
        if !response.status().is_success() {
            return Ok(response);
        }
        let (parts, body) = response.into_parts();
        let body = body
            .collect()
            .await
            .map_err(|error| S3Error::internal(format!("failed to read a listing: {error}")))?
            .to_bytes();
        let mut state = self.state.lock().expect("control operations cannot panic");
        if let Some(cached) = state.stale_listings.live_response(&cache_key) {
            return Ok(cached);
        }
        if state.stale_listings.generation == generation {
            state.stale_listings.listings.insert(
                cache_key,
                CachedListing {
                    expires_at: Instant::now() + ttl,
                    parts: parts.clone(),
                    body: body.clone(),
                },
            );
        }
        Ok(Response::from_parts(parts, full_body(body)))
    }
}

impl StaleListingCache {
    /// Drops expired listings and returns a copy of the live response cached for `cache_key`.
    fn live_response(&mut self, cache_key: &(String, String)) -> Option<S3Response> {
        let now = Instant::now();
        self.listings.retain(|_, cached| cached.expires_at > now);
        let cached = self.listings.get(cache_key)?;
        Some(Response::from_parts(
            cached.parts.clone(),
            full_body(cached.body.clone()),
        ))
    }
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use http::Response;
    use http_body_util::BodyExt;
    use tokio::sync::oneshot;

    use crate::config::ServerConfig;
    use crate::controls::Controls;
    use crate::s3_error::S3Error;
    use crate::server::{S3Response, full_body};

    const TTL: Duration = Duration::from_secs(600);

    async fn listed(receiver: oneshot::Receiver<&'static str>) -> Result<S3Response, S3Error> {
        let body = receiver.await.expect("the test sends a listing body");
        Ok(Response::new(full_body(body)))
    }

    async fn body_text(response: Result<S3Response, S3Error>) -> String {
        let bytes = response
            .expect("the listing succeeds")
            .into_body()
            .collect()
            .await
            .expect("the body is readable")
            .to_bytes();
        String::from_utf8(bytes.to_vec()).expect("the body is text")
    }

    #[tokio::test]
    async fn concurrent_first_listings_return_the_response_that_finished_first() {
        let controls = Controls::new(ServerConfig::default());
        let (older_sender, older_receiver) = oneshot::channel();
        let (newer_sender, newer_receiver) = oneshot::channel();
        let older = controls.serve_stale_listing("bucket", "prefix/", TTL, listed(older_receiver));
        let newer = controls.serve_stale_listing("bucket", "prefix/", TTL, listed(newer_receiver));
        // Both listings miss the empty cache before the driver lets either finish.
        let (older, newer, ()) = tokio::join!(older, newer, async {
            tokio::task::yield_now().await;
            older_sender.send("older").expect("the older listing waits");
            tokio::task::yield_now().await;
            newer_sender.send("newer").expect("the newer listing waits");
        });
        assert_eq!(body_text(older).await, "older");
        assert_eq!(
            body_text(newer).await,
            "older",
            "a listing that finishes later returns the cached response"
        );
    }

    #[tokio::test]
    async fn a_listing_that_started_before_a_reset_does_not_refill_the_cache() {
        let controls = Controls::new(ServerConfig::default());
        let (sender, receiver) = oneshot::channel();
        let before_reset = controls.serve_stale_listing("bucket", "prefix/", TTL, listed(receiver));
        let (before_reset, ()) = tokio::join!(before_reset, async {
            tokio::task::yield_now().await;
            controls
                .state
                .lock()
                .expect("control operations cannot panic")
                .stale_listings
                .clear();
            sender.send("before reset").expect("the listing waits");
        });
        assert_eq!(body_text(before_reset).await, "before reset");
        let (sender, receiver) = oneshot::channel();
        sender.send("after reset").expect("the listing waits");
        let after_reset = controls
            .serve_stale_listing("bucket", "prefix/", TTL, listed(receiver))
            .await;
        assert_eq!(body_text(after_reset).await, "after reset");
    }
}
