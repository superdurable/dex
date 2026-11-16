// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use super::map_wait;
use crate::server_protocol::NegotiatedServerProtocolVersion;
use crate::{
    Attribute, Channel, Condition, ConditionCombination, Context, Flow, HandlerResult,
    PersistenceSchema, Registry, Step, StepDecision, StepList, SubFlow, SubFlowOptions, Timer,
    Wait,
};

struct ConditionFlow {
    channel: Channel<()>,
}

impl Flow for ConditionFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&self.channel)
    }
}

struct TimeoutRecovery;

impl Step for TimeoutRecovery {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct HardDeleteSubFlowTarget {
    start: TimeoutRecovery,
    api_key: Attribute<String>,
}

impl Flow for HardDeleteSubFlowTarget {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&self.api_key)
    }
}

#[test]
fn sub_flow_hard_delete_attributes_require_protocol_version_three() {
    let api_key = Attribute::<String>::new("api-key").hard_delete_on_null();
    let target = HardDeleteSubFlowTarget {
        start: TimeoutRecovery,
        api_key: api_key.clone(),
    };
    let registry = Registry::new()
        .register(ConditionFlow {
            channel: Channel::new("commands"),
        })
        .and_then(|registry| {
            registry.register(HardDeleteSubFlowTarget {
                start: TimeoutRecovery,
                api_key: api_key.clone(),
            })
        })
        .expect("register SubFlow Flows");
    let parent = registry.flow("ConditionFlow").expect("lookup parent Flow");
    let sub_flow_wait = |attribute: &Attribute<String>| {
        Wait::until(
            SubFlow::run_with_options(
                &target,
                (),
                SubFlowOptions::new().initial_attribute(attribute, "key-1".to_string()),
            )
            .expect("SubFlow Condition"),
        )
    };

    for version in [0, 2] {
        let negotiated = NegotiatedServerProtocolVersion::default();
        negotiated.store(version);
        let error = map_wait(&registry, parent, &negotiated, sub_flow_wait(&api_key))
            .expect_err("old Server rejects SubFlow hard-delete writes");
        assert!(error.to_string().contains("protocol version 3 or later"));
    }

    let negotiated = NegotiatedServerProtocolVersion::default();
    negotiated.store(3);
    let error = map_wait(
        &registry,
        parent,
        &negotiated,
        sub_flow_wait(&Attribute::new("api-key")),
    )
    .expect_err("mismatched SubFlow definition");
    assert!(
        error
            .to_string()
            .contains("does not match its registered definition")
    );
    let condition = map_wait(&registry, parent, &negotiated, sub_flow_wait(&api_key))
        .expect("map SubFlow wait")
        .expect("waiting condition");
    let write = &condition.sub_flow_conditions[0]
        .options
        .as_ref()
        .expect("SubFlow options")
        .attributes[0];
    assert_eq!("api-key", write.key);
    assert!(write.is_hard_delete_on_null);
}

fn condition_registry() -> Registry {
    Registry::new()
        .register(ConditionFlow {
            channel: Channel::new("commands"),
        })
        .expect("register Condition Flow")
}

#[test]
fn maps_the_graceful_completion_condition_with_its_id() {
    let registry = condition_registry();
    let flow = registry.flow("ConditionFlow").expect("lookup Flow");
    let channel = Channel::<()>::new("commands");

    let any_of = map_wait(
        &registry,
        flow,
        &NegotiatedServerProtocolVersion::default(),
        Wait::any_of([
            channel.for_one(),
            Condition::graceful_completion_requested(),
        ]),
    )
    .expect("map AnyOf wait")
    .expect("waiting condition");
    assert_eq!(1, any_of.graceful_completion_conditions.len());
    assert_eq!("", any_of.graceful_completion_conditions[0].condition_id);

    let combination = map_wait(
        &registry,
        flow,
        &NegotiatedServerProtocolVersion::default(),
        Wait::any_combination_of([ConditionCombination::all_of([
            channel.for_one().with_id("message"),
            Condition::graceful_completion_requested().with_id("graceful"),
        ])]),
    )
    .expect("map combination wait")
    .expect("waiting condition");
    assert_eq!(
        "graceful",
        combination.graceful_completion_conditions[0].condition_id
    );
    assert_eq!(
        vec!["message", "graceful"],
        combination.condition_combinations[0].condition_ids
    );
}

#[test]
fn rejects_a_second_graceful_completion_condition_in_one_wait() {
    let registry = condition_registry();
    let flow = registry.flow("ConditionFlow").expect("lookup Flow");

    let error = map_wait(
        &registry,
        flow,
        &NegotiatedServerProtocolVersion::default(),
        Wait::any_of([
            Condition::graceful_completion_requested(),
            Timer::by_duration(Duration::from_secs(1)),
            Condition::graceful_completion_requested(),
        ]),
    )
    .expect_err("a second graceful completion condition must fail");
    assert!(
        error
            .to_string()
            .contains("at most one graceful completion condition")
    );

    let reused = Condition::graceful_completion_requested().with_id("graceful");
    let reused_wait = map_wait(
        &registry,
        flow,
        &NegotiatedServerProtocolVersion::default(),
        Wait::any_combination_of([
            ConditionCombination::all_of([reused.clone()]),
            ConditionCombination::all_of([reused]),
        ]),
    )
    .expect("one Condition reused across combinations")
    .expect("waiting condition");
    assert_eq!(1, reused_wait.graceful_completion_conditions.len());
}

#[test]
fn graceful_completion_condition_requires_protocol_version_seven() {
    let registry = condition_registry();
    let flow = registry.flow("ConditionFlow").expect("lookup Flow");
    let channel = Channel::<()>::new("commands");
    let graceful_wait = || {
        Wait::any_of([
            channel.for_one(),
            Condition::graceful_completion_requested(),
        ])
    };

    for version in [0, 4, 6] {
        let negotiated = NegotiatedServerProtocolVersion::default();
        negotiated.store(version);
        let waiting =
            map_wait(&registry, flow, &negotiated, graceful_wait()).expect("map graceful wait");
        let error = negotiated
            .require_graceful_completion_condition_support(waiting)
            .expect_err("an older Server rejects the graceful completion condition");
        assert_eq!(
            "dex_sdk::GracefulCompletionConditionUnsupportedError",
            error.error_type()
        );
        assert!(error.to_string().contains(&format!(
            "protocol 7 or later, but the Server negotiated {version}"
        )));
    }

    let negotiated = NegotiatedServerProtocolVersion::default();
    negotiated.store(6);
    let plain = map_wait(&registry, flow, &negotiated, Wait::until(channel.for_one()))
        .expect("map plain wait");
    assert!(
        negotiated
            .require_graceful_completion_condition_support(plain)
            .expect("a Wait without the condition passes on protocol 6")
            .is_some()
    );
    let skipped = negotiated
        .require_graceful_completion_condition_support(None)
        .expect("skip_immediately passes on protocol 6");
    assert!(skipped.is_none());

    negotiated.store(7);
    let waiting = negotiated
        .require_graceful_completion_condition_support(
            map_wait(&registry, flow, &negotiated, graceful_wait()).expect("map graceful wait"),
        )
        .expect("protocol 7 accepts the graceful completion condition")
        .expect("waiting condition");
    assert_eq!(1, waiting.graceful_completion_conditions.len());
}
