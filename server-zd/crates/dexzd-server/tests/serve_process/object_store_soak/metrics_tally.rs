// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Counters and the commit latency histogram summed over every process of a soak, from the last
//! scrape of each process.

use std::collections::BTreeMap;
use std::fmt;

const STALE_LISTING_FAMILY: &str = "dexzd_object_store_versioned_listing_stale_total";
const COMMIT_LATENCY_BUCKET_FAMILY: &str = "dexzd_commit_latency_seconds_bucket";
const OBJECT_ERRORS_FAMILY: &str = "dexzd_object_store_request_errors_total";

/// Sums of the soak's counters; each process contributes its last scrape.
#[derive(Clone, Debug, Default)]
pub(crate) struct MetricsTally {
    stale_listings: BTreeMap<String, f64>,
    object_errors: BTreeMap<String, f64>,
    /// Cumulative commit counts by upper bound in seconds.
    commit_latency: BTreeMap<String, f64>,
    scraped_processes: usize,
}

impl MetricsTally {
    /// Adds the last scrape `body` of one process that has ended or is about to be reported.
    pub(crate) fn add_process(&mut self, body: &str) {
        self.scraped_processes += 1;
        for (labels, value) in samples(body, STALE_LISTING_FAMILY) {
            *self
                .stale_listings
                .entry(label_value(&labels, "site"))
                .or_default() += value;
        }
        for (labels, value) in samples(body, OBJECT_ERRORS_FAMILY) {
            *self
                .object_errors
                .entry(label_value(&labels, "operation"))
                .or_default() += value;
        }
        for (labels, value) in samples(body, COMMIT_LATENCY_BUCKET_FAMILY) {
            *self
                .commit_latency
                .entry(label_value(&labels, "le"))
                .or_default() += value;
        }
    }

    /// Returns the upper bound of the bucket that holds quantile `quantile` of commit latencies.
    fn commit_latency_bound(&self, quantile: f64) -> Option<String> {
        let mut buckets: Vec<(f64, &String, f64)> = self
            .commit_latency
            .iter()
            .filter_map(|(bound, count)| Some((parse_bound(bound)?, bound, *count)))
            .collect();
        buckets.sort_by(|left, right| left.0.total_cmp(&right.0));
        let total = buckets.last()?.2;
        if total <= 0.0 {
            return None;
        }
        buckets
            .iter()
            .find(|(_, _, cumulative)| *cumulative >= quantile * total)
            .map(|(bound, label, _)| {
                if *bound >= 1e300 {
                    "above 2.5s".to_owned()
                } else {
                    format!("at most {label}s")
                }
            })
    }

    fn commit_count(&self) -> f64 {
        self.commit_latency
            .iter()
            .filter_map(|(bound, count)| Some((parse_bound(bound)?, *count)))
            .max_by(|left, right| left.0.total_cmp(&right.0))
            .map_or(0.0, |(_, count)| count)
    }
}

impl fmt::Display for MetricsTally {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{} commits over {} processes, p50 {}, p99 {}; versioned listing stale {:?}; object store request errors {:?}",
            self.commit_count(),
            self.scraped_processes,
            self.commit_latency_bound(0.5)
                .unwrap_or_else(|| "unknown".to_owned()),
            self.commit_latency_bound(0.99)
                .unwrap_or_else(|| "unknown".to_owned()),
            self.stale_listings,
            self.object_errors
        )
    }
}

/// Returns the label text and value of every sample of `family` in an OpenMetrics `body`.
fn samples(body: &str, family: &str) -> Vec<(String, f64)> {
    let prefix = format!("{family}{{");
    body.lines()
        .filter_map(|line| {
            let rest = line.strip_prefix(&prefix)?;
            let (labels, value) = rest.split_once("} ")?;
            Some((
                labels.to_owned(),
                value.split_whitespace().next()?.parse().ok()?,
            ))
        })
        .collect()
}

fn label_value(labels: &str, name: &str) -> String {
    labels
        .split(',')
        .find_map(|pair| {
            let (key, value) = pair.split_once('=')?;
            (key == name).then(|| value.trim_matches('"').to_owned())
        })
        .unwrap_or_default()
}

fn parse_bound(bound: &str) -> Option<f64> {
    if bound == "+Inf" {
        return Some(f64::INFINITY);
    }
    bound.parse().ok()
}
