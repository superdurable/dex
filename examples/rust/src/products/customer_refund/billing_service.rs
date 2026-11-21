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

//! A fake billing service. `customer-refund list-refunds` prints the refunds it recorded.

use std::fmt::{self, Display, Formatter};
use std::path::PathBuf;
use std::sync::atomic::{AtomicU32, Ordering};

use serde::{Deserialize, Serialize};

use super::json_ledger::{read_json_ledger, update_json_ledger};
use super::refund_config::BillingMode;

pub const DEFAULT_BILLING_LEDGER: &str = ".customer-refund/billing-refunds.json";

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct Order {
    pub order_id: String,
    pub description: String,
    pub amount_cents: u64,
    pub days_since_charge: u32,
    pub outage_hours: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct RecordedRefund {
    pub refund_id: String,
    pub order_id: String,
    pub amount_cents: u64,
    pub idempotency_key: String,
}

#[derive(Debug)]
pub enum BillingError {
    /// The payment provider did not answer in time; a retry may succeed.
    PaymentTimeout {
        order_id: String,
    },
    UnknownOrder {
        order_id: String,
    },
    Ledger {
        path: PathBuf,
        detail: String,
    },
}

impl BillingError {
    /// Returns the error type that Dex History shows for this failure.
    pub fn error_type(&self) -> &'static str {
        match self {
            Self::PaymentTimeout { .. } => "PaymentTimeout",
            Self::UnknownOrder { .. } => "UnknownOrder",
            Self::Ledger { .. } => "BillingLedgerError",
        }
    }
}

impl Display for BillingError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> fmt::Result {
        match self {
            Self::PaymentTimeout { order_id } => write!(
                formatter,
                "no answer from the payment provider for {order_id}"
            ),
            Self::UnknownOrder { order_id } => write!(formatter, "no order {order_id} in billing"),
            Self::Ledger { path, detail } => {
                write!(
                    formatter,
                    "read or write the billing ledger {}: {detail}",
                    path.display()
                )
            }
        }
    }
}

impl std::error::Error for BillingError {}

/// Keeps refunds in a JSON file, so a restarted Worker sees earlier refunds.
pub struct FakeBillingService {
    ledger_path: PathBuf,
    mode: BillingMode,
    payment_timeouts: AtomicU32,
    reused_refunds: AtomicU32,
}

impl FakeBillingService {
    pub fn new(ledger_path: impl Into<PathBuf>, mode: BillingMode) -> Self {
        Self {
            ledger_path: ledger_path.into(),
            mode,
            payment_timeouts: AtomicU32::new(0),
            reused_refunds: AtomicU32::new(0),
        }
    }

    pub fn get_order(&self, order_id: &str) -> Result<Order, BillingError> {
        let order = |description: &str, amount_cents, days_since_charge, outage_hours| Order {
            order_id: order_id.to_string(),
            description: description.to_string(),
            amount_cents,
            days_since_charge,
            outage_hours,
        };
        match order_id {
            "INV-1249" => Ok(order("Studio plan renewal", 124_900, 6, 31)),
            "INV-0388" => Ok(order("Studio plan renewal", 124_900, 47, 0)),
            "INV-0412" => Ok(order("Studio plan renewal", 124_900, 41, 31)),
            "INV-0517" => Ok(order("Extra seat", 4_900, 5, 0)),
            "INV-0049" => Ok(order("Extra seat", 4_900, 3, 31)),
            _ => Err(BillingError::UnknownOrder {
                order_id: order_id.to_string(),
            }),
        }
    }

    /// Refunds the order once per idempotency key and returns the refund ID.
    ///
    /// In the `call-missing` mode it returns an empty refund ID without a refund.
    pub fn refund(
        &self,
        order: &Order,
        idempotency_key: &str,
        simulate_timeout: bool,
    ) -> Result<String, BillingError> {
        if self.mode == BillingMode::CallMissing {
            println!("Billing call missing: no refund for {}", order.order_id);
            return Ok(String::new());
        }
        if simulate_timeout {
            self.payment_timeouts.fetch_add(1, Ordering::SeqCst);
            return Err(BillingError::PaymentTimeout {
                order_id: order.order_id.clone(),
            });
        }
        let (refund_id, is_new) =
            update_json_ledger(&self.ledger_path, |refunds: &mut Vec<RecordedRefund>| {
                if let Some(earlier) = refunds
                    .iter()
                    .find(|refund| refund.idempotency_key == idempotency_key)
                {
                    return (false, (earlier.refund_id.clone(), false));
                }
                let refund_id = format!("rf_{}", 1042 + refunds.len());
                refunds.push(RecordedRefund {
                    refund_id: refund_id.clone(),
                    order_id: order.order_id.clone(),
                    amount_cents: order.amount_cents,
                    idempotency_key: idempotency_key.to_string(),
                });
                (true, (refund_id, true))
            })
            .map_err(|detail| self.ledger_error(detail))?;
        if is_new {
            println!(
                "Refunded {} USD as {refund_id}",
                format_usd(order.amount_cents)
            );
        } else {
            self.reused_refunds.fetch_add(1, Ordering::SeqCst);
            println!("Refund {refund_id} already issued (key {idempotency_key})");
        }
        Ok(refund_id)
    }

    fn ledger_error(&self, detail: String) -> BillingError {
        BillingError::Ledger {
            path: self.ledger_path.clone(),
            detail,
        }
    }

    pub fn list_refunds(&self) -> Result<Vec<RecordedRefund>, BillingError> {
        read_json_ledger(&self.ledger_path).map_err(|detail| self.ledger_error(detail))
    }

    /// Returns how many simulated payment timeouts this service raised.
    pub fn payment_timeouts(&self) -> u32 {
        self.payment_timeouts.load(Ordering::SeqCst)
    }

    /// Returns how many refund calls this service answered with an earlier refund.
    pub fn reused_refunds(&self) -> u32 {
        self.reused_refunds.load(Ordering::SeqCst)
    }
}

/// Formats cents as US dollars with thousands separators, such as 1,249.00.
pub fn format_usd(amount_cents: u64) -> String {
    let dollars = (amount_cents / 100).to_string();
    let mut grouped = String::new();
    for (index, digit) in dollars.chars().enumerate() {
        if index > 0 && (dollars.len() - index).is_multiple_of(3) {
            grouped.push(',');
        }
        grouped.push(digit);
    }
    format!("{grouped}.{:02}", amount_cents % 100)
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;
    use std::sync::{Arc, Barrier};
    use std::thread;

    use super::{FakeBillingService, format_usd};
    use crate::products::customer_refund::refund_config::BillingMode;

    const CONCURRENT_CALLS: usize = 8;

    #[test]
    fn formats_amounts_with_thousands_separators() {
        assert_eq!(format_usd(124_900), "1,249.00");
        assert_eq!(format_usd(4_900), "49.00");
        assert_eq!(format_usd(123_456_789), "1,234,567.89");
    }

    /// Calls `refund` from several threads at once, each with the key `key_for(index)`.
    fn refund_concurrently(
        billing: &Arc<FakeBillingService>,
        key_for: fn(usize) -> String,
    ) -> Vec<String> {
        let order = billing.get_order("INV-1249").unwrap();
        let barrier = Arc::new(Barrier::new(CONCURRENT_CALLS));
        let calls: Vec<_> = (0..CONCURRENT_CALLS)
            .map(|index| {
                let billing = Arc::clone(billing);
                let barrier = Arc::clone(&barrier);
                let order = order.clone();
                thread::spawn(move || {
                    barrier.wait();
                    billing.refund(&order, &key_for(index), false).unwrap()
                })
            })
            .collect();
        calls.into_iter().map(|call| call.join().unwrap()).collect()
    }

    #[test]
    fn concurrent_refunds_with_one_key_record_one_refund() {
        let directory = tempfile::tempdir().unwrap();
        let ledger = directory.path().join("refunds.json");
        let billing = Arc::new(FakeBillingService::new(&ledger, BillingMode::Normal));

        let refund_ids = refund_concurrently(&billing, |_| "refund-same".to_string());

        let refunds = billing.list_refunds().unwrap();
        assert_eq!(refunds.len(), 1);
        assert!(refund_ids.iter().all(|id| *id == refunds[0].refund_id));
        assert_eq!(billing.reused_refunds(), (CONCURRENT_CALLS - 1) as u32);
        let restarted = FakeBillingService::new(&ledger, BillingMode::Normal);
        let order = restarted.get_order("INV-1249").unwrap();
        let again = restarted.refund(&order, "refund-same", false).unwrap();
        assert_eq!(again, refunds[0].refund_id);
    }

    #[test]
    fn concurrent_refunds_with_different_keys_record_every_refund() {
        let directory = tempfile::tempdir().unwrap();
        let billing = Arc::new(FakeBillingService::new(
            directory.path().join("refunds.json"),
            BillingMode::Normal,
        ));

        let refund_ids = refund_concurrently(&billing, |index| format!("refund-{index}"));

        let refunds = billing.list_refunds().unwrap();
        assert_eq!(refunds.len(), CONCURRENT_CALLS);
        let recorded: BTreeSet<_> = refunds.iter().map(|r| r.refund_id.clone()).collect();
        let returned: BTreeSet<_> = refund_ids.into_iter().collect();
        assert_eq!(recorded.len(), CONCURRENT_CALLS);
        assert_eq!(recorded, returned);
        let keys: BTreeSet<_> = refunds.iter().map(|r| r.idempotency_key.clone()).collect();
        assert_eq!(keys.len(), CONCURRENT_CALLS);
    }
}
