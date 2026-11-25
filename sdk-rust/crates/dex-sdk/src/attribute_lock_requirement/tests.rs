// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::{
    MissingAttributeLockAccess, MissingAttributeLockAccessKind, MissingAttributeLockError,
    MissingAttributeLockInvocation,
};

#[test]
fn error_message_names_handler_accesses_and_required_locks() {
    let error = MissingAttributeLockError {
        flow_type: "CartFlow".to_owned(),
        invocation: MissingAttributeLockInvocation::ExecuteMethod {
            step_type: "UpdateCart".to_owned(),
        },
        accesses: vec![
            MissingAttributeLockAccess {
                kind: MissingAttributeLockAccessKind::AttributeMapWrite,
                name: "items".to_owned(),
                instance: Some("item a".to_owned()),
                lock_attribute_name: "items".to_owned(),
                lock_instance: Some("item a".to_owned()),
            },
            MissingAttributeLockAccess {
                kind: MissingAttributeLockAccessKind::ChannelPublish,
                name: "events".to_owned(),
                instance: None,
                lock_attribute_name: "cart-lock".to_owned(),
                lock_instance: None,
            },
        ],
    };

    assert_eq!(
        "Flow \"CartFlow\" Step \"UpdateCart\" execute wrote AttributeMap \"items\" instance \
         \"item a\" without the required lock of AttributeMap \"items\" instance \"item a\", \
         published to Channel \"events\" without the required lock of Attribute \"cart-lock\"; \
         register the lock with StepOptions::wait_for_lock, StepOptions::execute_lock, or \
         RpcDefinition::lock",
        error.to_string()
    );
}

#[test]
fn rpc_error_message_names_the_rpc() {
    let error = MissingAttributeLockError {
        flow_type: "ChatFlow".to_owned(),
        invocation: MissingAttributeLockInvocation::RpcMethod {
            rpc_name: "reply".to_owned(),
        },
        accesses: vec![MissingAttributeLockAccess {
            kind: MissingAttributeLockAccessKind::AttributeWrite,
            name: "total".to_owned(),
            instance: None,
            lock_attribute_name: "total".to_owned(),
            lock_instance: None,
        }],
    };

    assert!(
        error.to_string().starts_with(
            "Flow \"ChatFlow\" RPC \"reply\" wrote Attribute \"total\" without the required lock \
             of Attribute \"total\";"
        ),
        "{error}"
    );
}
