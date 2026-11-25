// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;
use std::sync::atomic::Ordering;
use std::time::Duration;

use dex_sdk::{
    Attribute, AttributeMap, Channel, ChannelMap, Client, Flow, FlowStatus, PersistenceSchema,
    Registry, RpcInvokeOptions, SdkError, SdkResult, StartFlowOptions,
};

use crate::attribute_lock_requirement_workflow::{
    AttributeLockRequirementOptionsSnapshotFlow, AttributeLockRequirementRpcFlow,
    AttributeLockRequirementStepFlow, AttributeLockRequirementTimeoutFlow,
    DUPLICATE_EVENT_INSTANCE, LOCK_ONLY_ON_NEXT_OPTIONS_CALL, LOCK_REQ_FREE, LOCK_REQ_ITEMS,
    LOCK_REQ_SHARED, LOCK_REQ_TOTAL, OPTIONS_SNAPSHOT_GATE, SPACED_INSTANCE, WRITTEN,
};
use crate::support::{DexDevTestEnvironment, flow_id};

const MISSING_LOCK_ERROR_TYPE: &str = "dex_sdk::MissingAttributeLockError";
const FLOW_WAIT_TIMEOUT: Duration = Duration::from_secs(30);

/// Has the name and type of `LOCK_REQ_TOTAL` without its lock requirement.
static LOCK_REQ_TOTAL_UNDECLARED: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("lock-req-total"));
static LOCK_REQ_MISSING: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("lock-req-missing"));
static LOCK_REQ_MAP_LOCK: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("lock-req-map-lock"));
static LOCK_REQ_MAP_LOCK_COLLISION: LazyLock<AttributeMap<bool>> =
    LazyLock::new(|| AttributeMap::new("lock-req-map-lock"));
static LOCK_REQ_EMPTY_LOCK: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new(""));
static LOCK_REQ_MISSING_WRITER: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("lock-req-a").writes_require_attribute_lock(&LOCK_REQ_MISSING));
static LOCK_REQ_EMPTY_WRITER: LazyLock<AttributeMap<i64>> = LazyLock::new(|| {
    AttributeMap::new("lock-req-a").writes_require_attribute_lock(&LOCK_REQ_EMPTY_LOCK)
});
static LOCK_REQ_MISSING_PUBLISHER: LazyLock<ChannelMap<String>> = LazyLock::new(|| {
    ChannelMap::new("lock-req-c").publishes_require_attribute_lock(&LOCK_REQ_MISSING)
});
static LOCK_REQ_MAP_LOCK_PUBLISHER: LazyLock<Channel<String>> = LazyLock::new(|| {
    Channel::new("lock-req-c").publishes_require_attribute_lock(&LOCK_REQ_MAP_LOCK)
});
static LOCK_REQ_EMPTY_PUBLISHER: LazyLock<Channel<String>> = LazyLock::new(|| {
    Channel::new("lock-req-c").publishes_require_attribute_lock(&LOCK_REQ_EMPTY_LOCK)
});

fn lock_requirement_registry() -> SdkResult<Registry> {
    Registry::new()
        .register(AttributeLockRequirementStepFlow::default())?
        .register(AttributeLockRequirementRpcFlow)?
        .register(AttributeLockRequirementTimeoutFlow)
}

fn start_flow<SomeFlow: Flow>(
    client: &Client,
    flow: &SomeFlow,
    input: SomeFlow::StartInput,
) -> String {
    let flow_id = flow_id("rust-lock-req");
    client
        .start_flow(flow, &flow_id, input)
        .expect("start lock requirement Flow");
    flow_id
}

fn completed_output(client: &Client, flow_id: &str) -> String {
    let result = client
        .wait_for_flow_with_timeout(flow_id, FLOW_WAIT_TIMEOUT)
        .expect("lock requirement Flow closes");
    assert_eq!(
        FlowStatus::Completed,
        result.status(),
        "{:?}",
        result.error_message()
    );
    result.single_output().expect("decode Flow output")
}

fn is_total_set(client: &Client, flow_id: &str) -> bool {
    let (total,) = client
        .get_attributes(flow_id, (&*LOCK_REQ_TOTAL,))
        .expect("read total");
    total.is_some()
}

fn is_free_set(client: &Client, flow_id: &str) -> bool {
    let (free,) = client
        .get_attributes(flow_id, (&*LOCK_REQ_FREE,))
        .expect("read undeclared Attribute");
    free.is_some()
}

fn is_instance_set(
    client: &Client,
    flow_id: &str,
    attribute_map: &AttributeMap<i64>,
    instance: &str,
) -> bool {
    let (value,) = client
        .get_attributes(flow_id, (&attribute_map.instance(instance),))
        .expect("read AttributeMap instance");
    value.is_some()
}

fn assert_missing_lock<Output: std::fmt::Debug>(result: SdkResult<Output>, fragment: &str) {
    match result {
        Err(SdkError::WorkerInvocation { worker, .. }) => {
            assert_eq!(MISSING_LOCK_ERROR_TYPE, worker.error_type());
            assert!(
                worker.detail().contains(fragment),
                "detail {:?} lacks {fragment:?}",
                worker.detail()
            );
        }
        other => panic!("expected a missing lock Worker error, got {other:?}"),
    }
}

/// Covers declared writes from `wait_for` and `execute`.
#[test]
#[ignore = "requires dexcli dev"]
fn attribute_lock_requirement_steps() {
    let environment = DexDevTestEnvironment::start(lock_requirement_registry());
    let client = &environment.client;
    let flow = AttributeLockRequirementStepFlow::default();
    let expect_missing = |step_case: &str, fragment: &str| -> String {
        let flow_id = start_flow(client, &flow, step_case.to_string());
        let output = completed_output(client, &flow_id);
        assert!(
            output.starts_with(&format!("{MISSING_LOCK_ERROR_TYPE}: ")),
            "{step_case}: {output}"
        );
        assert!(output.contains(fragment), "{step_case}: {output}");
        flow_id
    };
    let expect_written = |step_case: &str| -> String {
        let flow_id = start_flow(client, &flow, step_case.to_string());
        assert_eq!(WRITTEN, completed_output(client, &flow_id), "{step_case}");
        flow_id
    };

    let flow_id = expect_missing(
        "unlocked-total",
        "Flow \"AttributeLockRequirementStepFlow\" Step \"LockReqUnlockedTotalStep\" execute \
         wrote Attribute \"lock-req-total\" without the required lock of Attribute \
         \"lock-req-total\"",
    );
    assert!(!is_total_set(client, &flow_id));

    let flow_id = expect_missing("movement-lock-total", "wrote Attribute \"lock-req-total\"");
    assert!(
        !is_total_set(client, &flow_id),
        "a per-movement lock does not satisfy the declared requirement"
    );

    let flow_id = expect_written("locked-total");
    assert!(is_total_set(client, &flow_id));

    let flow_id = expect_written("locked-item");
    assert!(is_instance_set(
        client,
        &flow_id,
        &LOCK_REQ_ITEMS,
        SPACED_INSTANCE
    ));

    let flow_id = expect_missing(
        "other-item",
        "wrote AttributeMap \"lock-req-items\" instance \"item b\" without the required lock of \
         AttributeMap \"lock-req-items\" instance \"item b\"",
    );
    assert!(
        !is_instance_set(client, &flow_id, &LOCK_REQ_ITEMS, SPACED_INSTANCE),
        "the failure also discards the locked instance write"
    );
    assert!(!is_instance_set(
        client,
        &flow_id,
        &LOCK_REQ_ITEMS,
        "item b"
    ));

    let flow_id = expect_written("locked-shared");
    assert!(is_instance_set(client, &flow_id, &LOCK_REQ_SHARED, "x"));

    let flow_id = expect_missing(
        "unlocked-shared",
        "wrote AttributeMap \"lock-req-shared\" instance \"x\" without the required lock of \
         Attribute \"lock-req-coordination\"",
    );
    assert!(!is_instance_set(client, &flow_id, &LOCK_REQ_SHARED, "x"));

    let flow_id = expect_written("undeclared");
    assert!(is_free_set(client, &flow_id));

    let flow_id = expect_missing(
        "wait-for-total",
        "Step \"LockReqWaitForTotalStep\" wait_for wrote Attribute \"lock-req-total\"",
    );
    assert!(
        !is_total_set(client, &flow_id),
        "the failed wait_for write is discarded"
    );

    let flow_id = expect_written("locked-wait-for-total");
    assert!(is_total_set(client, &flow_id));
}

/// Shows that a Flow timeout handler is not checked.
#[test]
#[ignore = "requires dexcli dev"]
fn attribute_lock_requirement_timeout_handler_is_exempt() {
    let environment = DexDevTestEnvironment::start(lock_requirement_registry());
    let client = &environment.client;
    let flow_id = flow_id("rust-lock-req-timeout");
    client
        .start_flow_with_options(
            &AttributeLockRequirementTimeoutFlow,
            &flow_id,
            (),
            StartFlowOptions::new().timeout(Duration::from_secs(1)),
        )
        .expect("start timeout Flow");
    assert_eq!("expired", completed_output(client, &flow_id));
    let (total,) = client
        .get_attributes(&flow_id, (&*LOCK_REQ_TOTAL,))
        .expect("read total");
    assert_eq!(Some(5), total);
}

/// Covers declared publishes and writes from RPCs.
#[test]
#[ignore = "requires dexcli dev"]
fn attribute_lock_requirement_rpcs() {
    use AttributeLockRequirementRpcFlow as RpcFlow;
    let environment = DexDevTestEnvironment::start(lock_requirement_registry());
    let client = &environment.client;

    // A locked publish commits, and an unlocked one is discarded with its other buffered write.
    let flow_id = start_flow(client, &RpcFlow, ());
    let reply_count = || {
        client
            .invoke_rpc_without_input(&flow_id, RpcFlow::COUNT_REPLIES)
            .expect("count replies")
    };
    client
        .invoke_rpc(&flow_id, RpcFlow::PUBLISH_REPLY_LOCKED, "hi".to_string())
        .expect("locked publish");
    assert_eq!(1, reply_count(), "the locked publish commits");
    assert_missing_lock(
        client.invoke_rpc(&flow_id, RpcFlow::PUBLISH_REPLY_UNLOCKED, "hi".to_string()),
        "Flow \"AttributeLockRequirementRpcFlow\" RPC \"publish_reply_unlocked\" published to \
         Channel \"lock-req-replies\" without the required lock of Attribute \
         \"lock-req-coordination\"",
    );
    assert_eq!(1, reply_count(), "the rejected publish is discarded");
    assert!(
        !is_free_set(client, &flow_id),
        "the other buffered write is discarded"
    );

    let flow_id = start_flow(client, &RpcFlow, ());
    assert_missing_lock(
        client.invoke_rpc(&flow_id, RpcFlow::SET_ITEM, SPACED_INSTANCE.to_string()),
        "wrote AttributeMap \"lock-req-items\" instance \"item a\"",
    );
    assert!(!is_instance_set(
        client,
        &flow_id,
        &LOCK_REQ_ITEMS,
        SPACED_INSTANCE
    ));

    let flow_id = start_flow(client, &RpcFlow, ());
    assert_missing_lock(
        client.invoke_rpc_with_options(
            &flow_id,
            RpcFlow::SET_ITEM,
            SPACED_INSTANCE.to_string(),
            RpcInvokeOptions::new()
                .lock_attribute_map_instance(LOCK_REQ_ITEMS.lock(SPACED_INSTANCE)),
        ),
        "wrote AttributeMap \"lock-req-items\" instance \"item a\"",
    );
    assert!(
        !is_instance_set(client, &flow_id, &LOCK_REQ_ITEMS, SPACED_INSTANCE),
        "a call-time lock does not satisfy the declared requirement"
    );

    let flow_id = start_flow(client, &RpcFlow, ());
    client
        .invoke_rpc(
            &flow_id,
            RpcFlow::SET_ITEM_LOCKED,
            SPACED_INSTANCE.to_string(),
        )
        .expect("registered lock satisfies the requirement");
    assert!(is_instance_set(
        client,
        &flow_id,
        &LOCK_REQ_ITEMS,
        SPACED_INSTANCE
    ));

    let flow_id = start_flow(client, &RpcFlow, ());
    assert_missing_lock(
        client.invoke_rpc(
            &flow_id,
            RpcFlow::PUBLISH_EVENT_UNLOCKED,
            SPACED_INSTANCE.to_string(),
        ),
        "published to ChannelMap \"lock-req-events\" instance \"item a\" without the required \
         lock of Attribute \"lock-req-coordination\"",
    );
    let event_count = client
        .invoke_rpc(&flow_id, RpcFlow::COUNT_EVENTS, SPACED_INSTANCE.to_string())
        .expect("count events");
    assert_eq!(0, event_count, "the rejected publish is discarded");
    assert!(
        !is_free_set(client, &flow_id),
        "the other buffered write is discarded"
    );

    let flow_id = start_flow(client, &RpcFlow, ());
    client
        .invoke_rpc(&flow_id, RpcFlow::SET_TOTAL_LOCKED, 7)
        .expect("locked write");
    assert_missing_lock(
        client.invoke_rpc_without_input(&flow_id, RpcFlow::DELETE_TOTAL_UNLOCKED),
        "RPC \"delete_total_unlocked\" wrote Attribute \"lock-req-total\" without the required \
         lock of Attribute \"lock-req-total\"",
    );
    let (total,) = client
        .get_attributes(&flow_id, (&*LOCK_REQ_TOTAL,))
        .expect("read total");
    assert_eq!(Some(7), total, "the rejected delete is discarded");
    let (undeclared_total,) = client
        .get_attributes(&flow_id, (&*LOCK_REQ_TOTAL_UNDECLARED,))
        .expect("an undeclared definition of the same Attribute still reads");
    assert_eq!(Some(7), undeclared_total);

    // A ChannelMap publish with the registered coordination lock commits.
    let flow_id = start_flow(client, &RpcFlow, ());
    client
        .invoke_rpc(
            &flow_id,
            RpcFlow::PUBLISH_EVENT_LOCKED,
            SPACED_INSTANCE.to_string(),
        )
        .expect("locked ChannelMap publish");
    let event_count = client
        .invoke_rpc(&flow_id, RpcFlow::COUNT_EVENTS, SPACED_INSTANCE.to_string())
        .expect("count events");
    assert_eq!(1, event_count, "the locked publish commits");

    // An unlocked AttributeMap delete fails and keeps the stored instance.
    let flow_id = start_flow(client, &RpcFlow, ());
    client
        .invoke_rpc(
            &flow_id,
            RpcFlow::SET_ITEM_LOCKED,
            SPACED_INSTANCE.to_string(),
        )
        .expect("locked write");
    assert_missing_lock(
        client.invoke_rpc(
            &flow_id,
            RpcFlow::DELETE_ITEM_UNLOCKED,
            SPACED_INSTANCE.to_string(),
        ),
        "RPC \"delete_item_unlocked\" wrote AttributeMap \"lock-req-items\" instance \"item a\"",
    );
    assert!(
        is_instance_set(client, &flow_id, &LOCK_REQ_ITEMS, SPACED_INSTANCE),
        "the rejected delete is discarded"
    );

    // Repeated writes and publishes report each key once, writes before publishes.
    let flow_id = start_flow(client, &RpcFlow, ());
    let detail = match client.invoke_rpc_without_input(&flow_id, RpcFlow::WRITE_DUPLICATES_UNLOCKED)
    {
        Err(SdkError::WorkerInvocation { worker, .. }) => {
            assert_eq!(MISSING_LOCK_ERROR_TYPE, worker.error_type());
            worker.detail().to_owned()
        }
        other => panic!("expected a missing lock Worker error, got {other:?}"),
    };
    let accesses = [
        "wrote AttributeMap \"lock-req-items\" instance \"item a\"",
        "wrote AttributeMap \"lock-req-items\" instance \"item b\"",
        "published to ChannelMap \"lock-req-events\" instance \"event a\"",
    ];
    let mut previous_position = 0;
    for access in accesses {
        assert_eq!(
            1,
            detail.matches(access).count(),
            "{access:?} in {detail:?}"
        );
        let position = detail.find(access).expect("access is reported");
        assert!(
            position >= previous_position,
            "order of {access:?} in {detail:?}"
        );
        previous_position = position;
    }
    assert!(!is_instance_set(
        client,
        &flow_id,
        &LOCK_REQ_ITEMS,
        SPACED_INSTANCE
    ));
    assert!(!is_instance_set(
        client,
        &flow_id,
        &LOCK_REQ_ITEMS,
        "item b"
    ));
    let event_count = client
        .invoke_rpc(
            &flow_id,
            RpcFlow::COUNT_EVENTS,
            DUPLICATE_EVENT_INSTANCE.to_string(),
        )
        .expect("count events");
    assert_eq!(0, event_count, "the rejected publishes are discarded");

    // Initial Attributes of a Flow start are not checked.
    let initial_flow_id = crate::support::flow_id("rust-lock-req-initial");
    client
        .start_flow_with_options(
            &RpcFlow,
            &initial_flow_id,
            (),
            StartFlowOptions::new().initial_attribute(&LOCK_REQ_TOTAL, 3),
        )
        .expect("start with a declared initial Attribute");
    let (total,) = client
        .get_attributes(&initial_flow_id, (&*LOCK_REQ_TOTAL,))
        .expect("read total");
    assert_eq!(Some(3), total);
}

/// A normal Flow start dispatches the Step with the lock fields read at registration.
#[test]
#[ignore = "requires dexcli dev"]
fn attribute_lock_requirement_dispatch_uses_registered_step_locks() {
    use AttributeLockRequirementOptionsSnapshotFlow as SnapshotFlow;
    OPTIONS_SNAPSHOT_GATE.reset();
    LOCK_ONLY_ON_NEXT_OPTIONS_CALL.store(true, Ordering::SeqCst);
    let registry = Registry::new().register(SnapshotFlow::default());
    assert!(
        !LOCK_ONLY_ON_NEXT_OPTIONS_CALL.load(Ordering::SeqCst),
        "registration reads the Step options"
    );
    let environment = DexDevTestEnvironment::start(registry);
    let client = &environment.client;
    let flow_id = start_flow(client, &SnapshotFlow::default(), ());
    assert!(
        OPTIONS_SNAPSHOT_GATE.wait_until_entered(FLOW_WAIT_TIMEOUT),
        "the Step started"
    );
    let conflict = client.invoke_rpc(&flow_id, SnapshotFlow::SET_TOTAL_LOCKED, 9);
    OPTIONS_SNAPSHOT_GATE.release();
    assert!(
        matches!(conflict, Err(SdkError::RpcLockConflict { .. })),
        "the running Step holds the registered lock, got {conflict:?}"
    );
    assert_eq!(WRITTEN, completed_output(client, &flow_id));
    let (total,) = client
        .get_attributes(&flow_id, (&*LOCK_REQ_TOTAL,))
        .expect("read total");
    assert_eq!(Some(1), total);
}

struct LockReqInvalidFlow {
    schema: PersistenceSchema,
}

impl Flow for LockReqInvalidFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        self.schema.clone()
    }
}

/// Rejects a lock Attribute that is missing, empty, or an AttributeMap.
#[test]
fn attribute_lock_requirement_registration_rejects_invalid_lock_attributes() {
    let cases = [
        (
            PersistenceSchema::new().attribute(&LOCK_REQ_MISSING_WRITER),
            "Attribute \"lock-req-a\" write lock: lock Attribute \"lock-req-missing\" is not in \
             the PersistenceSchema",
        ),
        (
            PersistenceSchema::new().channel_map(&LOCK_REQ_MISSING_PUBLISHER),
            "ChannelMap \"lock-req-c\" publish lock: lock Attribute \"lock-req-missing\" is not \
             in the PersistenceSchema",
        ),
        (
            PersistenceSchema::new()
                .attribute_map(&LOCK_REQ_MAP_LOCK_COLLISION)
                .channel(&LOCK_REQ_MAP_LOCK_PUBLISHER),
            "lock Attribute \"lock-req-map-lock\" is an AttributeMap; use a singleton Attribute",
        ),
        (
            PersistenceSchema::new().attribute_map(&LOCK_REQ_EMPTY_WRITER),
            "AttributeMap \"lock-req-a\" write lock: lock Attribute has an empty name",
        ),
        (
            PersistenceSchema::new().channel(&LOCK_REQ_EMPTY_PUBLISHER),
            "Channel \"lock-req-c\" publish lock: lock Attribute has an empty name",
        ),
    ];
    for (schema, expected) in cases {
        match Registry::new().register(LockReqInvalidFlow { schema }) {
            Err(SdkError::FlowDefinition { message }) => {
                assert!(message.contains(expected), "{message:?} lacks {expected:?}");
            }
            Err(error) => panic!("expected a Flow definition error, got {error}"),
            Ok(_) => panic!("registration accepted an invalid lock Attribute: {expected}"),
        }
    }
}
