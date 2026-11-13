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
}

struct CachedListing {
    expires_at: Instant,
    parts: http::response::Parts,
    body: Bytes,
}

impl StaleListingCache {
    pub(crate) fn clear(&mut self) {
        self.listings.clear();
    }
}

impl Controls {
    /// Serves the cached response of the first successful listing of `prefix` until `ttl` passes.
    ///
    /// Later listings of the same bucket and exact prefix receive that response unchanged, so
    /// they ignore `max-keys`, `start-after`, and continuation tokens. Hits do not extend the
    /// lifetime. Returns `list` when nothing is cached, caching its response when it succeeds.
    pub(crate) async fn serve_stale_listing(
        &self,
        bucket: &str,
        prefix: &str,
        ttl: Duration,
        list: impl Future<Output = Result<S3Response, S3Error>>,
    ) -> Result<S3Response, S3Error> {
        let cache_key = (bucket.to_owned(), prefix.to_owned());
        if let Some(response) = self.cached_listing(&cache_key) {
            return Ok(response);
        }
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
        let now = Instant::now();
        let is_cached = state
            .stale_listings
            .listings
            .get(&cache_key)
            .is_some_and(|cached| cached.expires_at > now);
        if !is_cached {
            state.stale_listings.listings.insert(
                cache_key,
                CachedListing {
                    expires_at: now + ttl,
                    parts: parts.clone(),
                    body: body.clone(),
                },
            );
        }
        Ok(Response::from_parts(parts, full_body(body)))
    }

    fn cached_listing(&self, cache_key: &(String, String)) -> Option<S3Response> {
        let mut state = self.state.lock().expect("control operations cannot panic");
        let now = Instant::now();
        state
            .stale_listings
            .listings
            .retain(|_, cached| cached.expires_at > now);
        let cached = state.stale_listings.listings.get(cache_key)?;
        Some(Response::from_parts(
            cached.parts.clone(),
            full_body(cached.body.clone()),
        ))
    }
}
