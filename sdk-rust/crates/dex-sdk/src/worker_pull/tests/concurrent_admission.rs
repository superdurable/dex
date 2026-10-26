// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

#[test]
fn concurrent_step_admission_preserves_rpc_reserve() {
    let shared = Arc::new(reserved_rpc_shared(1));
    let barrier = Arc::new(std::sync::Barrier::new(20));
    let contenders: Vec<_> = (1..=20)
        .map(|stream_id| {
            let shared = Arc::clone(&shared);
            let barrier = Arc::clone(&barrier);
            std::thread::spawn(move || {
                let task = PollWorkerTask {
                    request: Some(TaskRequest::Execute(Default::default())),
                    ..Default::default()
                };
                barrier.wait();
                shared.try_accept(stream_id, Some(&task))
            })
        })
        .collect();
    assert_eq!(
        1,
        contenders
            .into_iter()
            .map(|contender| usize::from(contender.join().expect("admission thread")))
            .sum::<usize>()
    );
    assert_eq!(1, shared.state.lock().expect("state lock").busy_tasks);
    assert_eq!(19, shared.reserved_rpc_declines.load(Ordering::Relaxed));
}
