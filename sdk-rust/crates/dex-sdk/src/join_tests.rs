// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::HashMap;

use dex_protocol::dex::Context as ProtoContext;

use super::{Join, JoinRound};
use crate::context::{ContextInput, InvocationCancellation, InvocationMethod};
use crate::server_protocol::NegotiatedServerProtocolVersion;
use crate::wait::ConditionKind;
use crate::{
    Condition, Context, Flow, HandlerResult, PersistenceSchema, Registry, Step, StepDecision,
    StepList, StepMovement,
};

static QUOTES: std::sync::LazyLock<Join<String>> = std::sync::LazyLock::new(|| Join::new("quotes"));
static BIDS: std::sync::LazyLock<Join<String>> = std::sync::LazyLock::new(|| Join::new("bids"));

struct Branch;

impl Step for Branch {
    type Input = JoinRound;

    fn execute(&self, _context: &mut Context, _input: JoinRound) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct JoinStep;

impl Step for JoinStep {
    type Input = JoinRound;

    fn execute(&self, _context: &mut Context, _input: JoinRound) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct JoinUnitFlow;

impl Flow for JoinUnitFlow {
    type StartInput = JoinRound;

    fn steps(&self) -> StepList<'_, JoinRound> {
        StepList::start(&Branch).and(&JoinStep)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().join(&QUOTES).join(&BIDS)
    }
}

fn context_with_step_execution_id(step_execution_id: &str) -> Context {
    let registry = Registry::new()
        .register(JoinUnitFlow)
        .expect("register Join unit Flow");
    let flow = registry
        .flow("JoinUnitFlow")
        .expect("lookup Join unit Flow")
        .clone();
    Context::new(
        ContextInput {
            method: InvocationMethod::Execute,
            flow,
            metadata: ProtoContext {
                step_execution_id: step_execution_id.to_string(),
                ..ProtoContext::default()
            },
            attributes: Vec::new(),
            locals: Vec::new(),
            condition_results: None,
            channel_infos: HashMap::new(),
            loaded_channel_messages: HashMap::new(),
            loaded_attribute_map_instances: Vec::new(),
            loaded_channel_names: Vec::new(),
            loaded_channel_map_instances: Vec::new(),
            negotiated_protocol_version: NegotiatedServerProtocolVersion::default(),
        },
        None,
        InvocationCancellation::new(),
    )
    .expect("create Join unit Context")
}

type BranchBuilder<'builder> = Box<dyn FnOnce(&JoinRound) -> StepMovement + 'builder>;

fn branch_builders(count: usize, observed_rounds: &mut Vec<JoinRound>) -> Vec<BranchBuilder<'_>> {
    let observed_rounds = std::cell::RefCell::new(observed_rounds);
    let observed_rounds = std::rc::Rc::new(observed_rounds);
    (0..count)
        .map(|_| {
            let observed_rounds = std::rc::Rc::clone(&observed_rounds);
            Box::new(move |round: &JoinRound| {
                observed_rounds.borrow_mut().push(round.clone());
                StepMovement::to(&Branch, round.clone())
            }) as BranchBuilder<'_>
        })
        .collect()
}

fn round(round_key: &str, branch_count: usize) -> JoinRound {
    JoinRound {
        round_key: round_key.to_string(),
        branch_count,
    }
}

fn channel_bounds(condition: &Condition) -> (Option<&str>, Option<usize>, Option<usize>) {
    match &condition.kind {
        ConditionKind::Channel {
            instance,
            at_least,
            at_most,
            ..
        } => (instance.as_deref(), *at_least, *at_most),
        _ => panic!("expected a Channel condition"),
    }
}

fn invalid_reason(condition: &Condition) -> &str {
    match &condition.kind {
        ConditionKind::Invalid(reason) => reason,
        _ => panic!("expected an invalid condition"),
    }
}

#[test]
fn fork_uses_step_execution_id_and_builder_count() {
    let mut context = context_with_step_execution_id("RequestQuotes-2");
    let mut observed_rounds = Vec::new();
    let mut join_round = None;
    let movements = QUOTES
        .fork(
            &mut context,
            branch_builders(3, &mut observed_rounds),
            |round| {
                join_round = Some(round.clone());
                StepMovement::to(&JoinStep, round)
            },
        )
        .expect("fork three branches");

    let expected = round("RequestQuotes-2", 3);
    assert_eq!(vec![expected.clone(); 3], observed_rounds);
    assert_eq!(Some(expected), join_round);
    let targets = movements
        .iter()
        .map(|movement| movement.target_step_type)
        .collect::<Vec<_>>();
    assert_eq!(vec!["Branch", "Branch", "Branch", "JoinStep"], targets);
}

#[test]
fn fork_without_branches_creates_an_immediately_complete_round() {
    let mut context = context_with_step_execution_id("ChooseReviewers-1");
    let mut join_round = None;
    let movements = QUOTES
        .fork(&mut context, Vec::new(), |round| {
            join_round = Some(round.clone());
            StepMovement::to(&JoinStep, round)
        })
        .expect("fork zero branches");

    assert_eq!(1, movements.len());
    let join_round = join_round.expect("join builder ran");
    assert_eq!(round("ChooseReviewers-1", 0), join_round);
    assert_eq!(
        (Some("ChooseReviewers-1"), Some(0), Some(0)),
        channel_bounds(&QUOTES.for_all_branches(&join_round))
    );
}

#[test]
fn fork_rejects_a_second_fork_of_the_same_join_in_one_invocation() {
    let mut context = context_with_step_execution_id("RequestQuotes-1");
    QUOTES
        .fork(&mut context, Vec::new(), |round| {
            StepMovement::to(&JoinStep, round)
        })
        .expect("first fork");
    BIDS.fork(&mut context, Vec::new(), |round| {
        StepMovement::to(&JoinStep, round)
    })
    .expect("another Join may fork");

    let error = QUOTES
        .fork(&mut context, Vec::new(), |round| {
            StepMovement::to(&JoinStep, round)
        })
        .err()
        .expect("second fork fails");
    assert!(error.to_string().contains("already forked"), "{error}");
}

#[test]
fn fork_requires_a_step_execution_id() {
    let mut context = context_with_step_execution_id("");
    let error = QUOTES
        .fork(&mut context, Vec::new(), |round| {
            StepMovement::to(&JoinStep, round)
        })
        .err()
        .expect("fork without a Step execution ID fails");
    assert!(error.to_string().contains("fork_with_round_key"), "{error}");
}

#[test]
fn fork_with_round_key_validates_the_key() {
    let mut observed_rounds = Vec::new();
    let movements = QUOTES
        .fork_with_round_key(
            "round-1",
            branch_builders(2, &mut observed_rounds),
            |round| StepMovement::to(&JoinStep, round),
        )
        .expect("fork with an explicit key");
    assert_eq!(3, movements.len());
    assert_eq!(vec![round("round-1", 2); 2], observed_rounds);

    for invalid_key in ["", "tenant/round"] {
        let error = QUOTES
            .fork_with_round_key(invalid_key, Vec::new(), |round| {
                StepMovement::to(&JoinStep, round)
            })
            .err()
            .expect("invalid key fails");
        assert!(
            error.to_string().contains("round key is invalid"),
            "{error}"
        );
    }
}

#[test]
fn conditions_count_the_round_results() {
    let join_round = round("RequestQuotes-1", 3);
    let instance = Some("RequestQuotes-1");

    assert_eq!(
        (instance, Some(3), Some(3)),
        channel_bounds(&QUOTES.for_all_branches(&join_round))
    );
    assert_eq!(
        (instance, Some(1), Some(1)),
        channel_bounds(&QUOTES.for_first_branch(&join_round))
    );
    assert_eq!(
        (instance, Some(2), Some(2)),
        channel_bounds(&QUOTES.for_branches(&join_round, 2))
    );
    assert_eq!(
        (instance, Some(1), Some(1)),
        channel_bounds(&QUOTES.for_remaining_branches(&join_round, 2))
    );
    assert_eq!(
        (instance, Some(0), Some(0)),
        channel_bounds(&QUOTES.for_remaining_branches(&join_round, 3))
    );
}

#[test]
fn conditions_defer_validation_errors_to_the_wait() {
    let join_round = round("RequestQuotes-1", 3);

    assert!(invalid_reason(&QUOTES.for_branches(&join_round, 0)).contains("outside 1..=3"));
    assert!(invalid_reason(&QUOTES.for_branches(&join_round, 4)).contains("outside 1..=3"));
    assert!(
        invalid_reason(&QUOTES.for_remaining_branches(&join_round, 4)).contains("outside 0..=3")
    );
    assert!(
        invalid_reason(&QUOTES.for_first_branch(&round("Empty-1", 0))).contains("outside 1..=0")
    );
    for invalid_key in ["", "tenant/round"] {
        let invalid_round = round(invalid_key, 3);
        for condition in [
            QUOTES.for_all_branches(&invalid_round),
            QUOTES.for_first_branch(&invalid_round),
            QUOTES.for_branches(&invalid_round, 2),
            QUOTES.for_remaining_branches(&invalid_round, 2),
        ] {
            assert!(invalid_reason(&condition).contains("round key is invalid"));
        }
    }
}

#[test]
fn report_branch_result_rejects_an_invalid_round_key() {
    let mut context = context_with_step_execution_id("QuoteFromSupplier-1");
    QUOTES
        .report_branch_result(&mut context, &round("RequestQuotes-1", 3), "A".to_string())
        .expect("report a valid round");
    assert!(
        QUOTES
            .end_branch(&mut context, &round("tenant/round", 3), "B".to_string())
            .is_err()
    );
    assert!(QUOTES.branch_results(&context, &round("", 3)).is_err());
}
