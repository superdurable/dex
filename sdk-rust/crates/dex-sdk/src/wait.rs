// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::any::TypeId;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::Arc;
use std::time::Duration;

use dex_protocol::dex::{GracefulCompletionCondition, Value as ProtoValue};

use crate::{HandlerError, HandlerResult, SubFlowOptions};

/// Describes when Dex may invoke a Step's `execute` handler.
///
/// Build waits from durable timer and Channel [`Condition`] values. Combinators consume their
/// inputs and preserve order, which determines timer indexes used by [`crate::TimerId`].
///
/// # Examples
///
/// ```
/// use dex_sdk::{Channel, Timer, Wait};
/// use std::time::Duration;
///
/// let approvals = Channel::<String>::new("approvals");
/// let wait = Wait::any_of([
///     approvals.for_one(),
///     Timer::by_duration(Duration::from_secs(300)),
/// ]);
/// ```
pub struct Wait {
    pub(crate) kind: WaitKind,
}

pub(crate) enum WaitKind {
    SkipImmediately,
    AllOf(Vec<Condition>),
    AnyOf(Vec<Condition>),
    AnyCombinationOf(Vec<ConditionCombination>),
}

impl Wait {
    /// Skips `wait_for` and asks Dex to invoke `execute` immediately.
    pub fn skip_immediately() -> Self {
        Self {
            kind: WaitKind::SkipImmediately,
        }
    }

    /// Waits until one condition is satisfied. The Condition does not need an ID.
    pub fn until(condition: Condition) -> Self {
        Self::all_of([condition])
    }

    /// Waits until every condition is satisfied. Conditions do not need IDs.
    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            kind: WaitKind::AllOf(conditions.into_iter().collect()),
        }
    }

    /// Waits until at least one condition is satisfied. Conditions do not need IDs.
    ///
    /// Among ready conditions, Dex selects the first in canonical order: Timers, Channels,
    /// SubFlows, then the graceful completion condition, preserving iterator order within each
    /// kind. An unready condition does not block a later ready condition. To wait for strict
    /// priority, return only the higher-priority condition.
    ///
    /// Channel consumption is not greedy across alternatives. Only the selected Channel condition
    /// consumes messages; other ready Channel conditions consume nothing.
    pub fn any_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            kind: WaitKind::AnyOf(conditions.into_iter().collect()),
        }
    }

    /// Waits for every Condition in any combination.
    ///
    /// Channel consumption is limited to Channel conditions in the selected combination.
    /// Conditions belonging only to other ready combinations consume nothing.
    ///
    /// Every Condition needs a non-empty user ID. Clones of the same Condition
    /// may be reused across combinations; distinct Conditions need unique IDs.
    pub fn any_combination_of(
        combinations: impl IntoIterator<Item = ConditionCombination>,
    ) -> Self {
        Self {
            kind: WaitKind::AnyCombinationOf(combinations.into_iter().collect()),
        }
    }
}

/// Represents one durable timer, Channel, SubFlow, or graceful completion predicate.
///
/// Conditions are created by [`crate::Timer`], [`crate::Channel`], [`crate::ChannelMap`],
/// [`crate::Join`], or [`Condition::graceful_completion_requested`]. Assign an ID when
/// identifying a timer independently or using [`Wait::any_combination_of`].
#[derive(Clone)]
pub struct Condition {
    pub(crate) identity: Arc<ConditionIdentity>,
    pub(crate) id: Option<String>,
    pub(crate) kind: ConditionKind,
}

pub(crate) struct ConditionIdentity;

#[derive(Clone)]
pub(crate) enum ConditionKind {
    Timer(Duration),
    Channel {
        name: String,
        instance: Option<String>,
        at_least: Option<usize>,
        at_most: Option<usize>,
    },
    SubFlow(Box<SubFlowDefinition>),
    GracefulCompletion,
    /// Fails Wait mapping with this reason; builders defer validation errors this way.
    Invalid(String),
}

#[derive(Clone)]
pub(crate) struct SubFlowDefinition {
    pub(crate) flow_type: &'static str,
    pub(crate) type_id: TypeId,
    pub(crate) input: ProtoValue,
    pub(crate) options: SubFlowOptions,
}

impl Condition {
    pub(crate) fn timer(duration: Duration) -> Self {
        Self {
            identity: Arc::new(ConditionIdentity),
            id: None,
            kind: ConditionKind::Timer(duration),
        }
    }

    pub(crate) fn channel(
        name: String,
        instance: Option<String>,
        at_least: Option<usize>,
        at_most: Option<usize>,
    ) -> Self {
        Self {
            identity: Arc::new(ConditionIdentity),
            id: None,
            kind: ConditionKind::Channel {
                name,
                instance,
                at_least,
                at_most,
            },
        }
    }

    pub(crate) fn sub_flow(definition: SubFlowDefinition) -> Self {
        let id = definition.options.condition_id.clone();
        Self {
            identity: Arc::new(ConditionIdentity),
            id,
            kind: ConditionKind::SubFlow(Box::new(definition)),
        }
    }

    pub(crate) fn invalid(reason: String) -> Self {
        Self {
            identity: Arc::new(ConditionIdentity),
            id: None,
            kind: ConditionKind::Invalid(reason),
        }
    }

    /// Returns a Condition satisfied once the Flow has a graceful completion request.
    ///
    /// A graceful completion request comes from [`crate::Client::stop_flow`] with
    /// [`crate::StopFlowOptions::graceful_complete`], or from a Step that returns
    /// [`crate::StepDecision::graceful_complete`]. The request stays in force, so a Wait
    /// registered after it is satisfied at once. When the request arrives, Dex re-checks only the
    /// Waits that include this Condition. Other waiting Steps keep waiting and keep the Flow
    /// running.
    ///
    /// Use it to let a long-waiting Step end when someone asks the Flow to complete. A typical
    /// Step waits for the next message or this Condition with [`Wait::any_of`]. Its `execute`
    /// first handles a message that the Wait consumed. When there is none and
    /// [`crate::Context::is_graceful_completion_condition_completed`] returns `true`, it returns
    /// [`crate::StepDecision::dead_end`]. The Flow then completes once no other Step is active.
    ///
    /// In [`Wait::any_of`], this Condition comes after Timers, Channels, and SubFlows, so a ready
    /// message still wins over the request. This Condition still reports completed when a message
    /// won and a request exists, so read the consumed messages first or they are lost. It also
    /// works in [`Wait::all_of`] and in [`Wait::any_combination_of`], where it needs an ID from
    /// [`Condition::with_id`]. A Wait may hold at most one graceful completion condition. A
    /// second one fails the Step's `wait_for` with an invalid-result error.
    ///
    /// It requires Server protocol version 7 or later, which server-zd 0.1.34 and later releases
    /// provide. With an older Server, the Step's `wait_for` fails with a
    /// [`GracefulCompletionConditionUnsupportedError`], and Dex retries it according to the
    /// Step's retry policy.
    ///
    /// # Examples
    ///
    /// ```no_run
    /// use std::sync::LazyLock;
    ///
    /// use dex_sdk::{Channel, Condition, Context, HandlerResult, Step, StepDecision, Wait};
    ///
    /// static USER_MESSAGES: LazyLock<Channel<String>> =
    ///     LazyLock::new(|| Channel::new("user-messages"));
    ///
    /// struct WaitForMessage;
    ///
    /// impl Step for WaitForMessage {
    ///     type Input = ();
    ///
    ///     fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
    ///         Ok(Wait::any_of([
    ///             USER_MESSAGES.for_one(),
    ///             Condition::graceful_completion_requested(),
    ///         ]))
    ///     }
    ///
    ///     fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
    ///         let messages = USER_MESSAGES.condition_results(context)?;
    ///         if let Some(message) = messages.first() {
    ///             println!("handling {message}");
    ///             return Ok(StepDecision::go_to(self, ()));
    ///         }
    ///         if context.is_graceful_completion_condition_completed() {
    ///             return Ok(StepDecision::dead_end());
    ///         }
    ///         Ok(StepDecision::go_to(self, ()))
    ///     }
    /// }
    /// ```
    ///
    /// Since 1.45.3.
    pub fn graceful_completion_requested() -> Self {
        Self {
            identity: Arc::new(ConditionIdentity),
            id: None,
            kind: ConditionKind::GracefulCompletion,
        }
    }

    /// Assigns a stable ID to a timer, Channel, SubFlow, or graceful completion Condition.
    ///
    /// [`crate::TimerId::by_condition_id`] targets a timer by this ID, and
    /// [`Wait::any_combination_of`] requires it on every Condition.
    pub fn with_id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }
}

/// Collects the graceful completion condition of one Wait; a Wait accepts at most one.
#[derive(Default)]
pub(crate) struct GracefulCompletionConditions {
    conditions: Vec<GracefulCompletionCondition>,
}

impl GracefulCompletionConditions {
    pub(crate) fn add(&mut self, condition_id: String) -> HandlerResult<()> {
        if !self.conditions.is_empty() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "a Wait accepts at most one graceful completion condition",
            ));
        }
        self.conditions
            .push(GracefulCompletionCondition { condition_id });
        Ok(())
    }

    pub(crate) fn into_conditions(self) -> Vec<GracefulCompletionCondition> {
        self.conditions
    }
}

/// Reports a Wait with a graceful completion condition for a Server that cannot evaluate it.
///
/// [`Condition::graceful_completion_requested`] requires Server protocol version 7 or later,
/// which server-zd 0.1.34 and later releases provide. An older Server would drop the condition,
/// so a Wait with only that condition would not wait at all. The SDK refuses instead and sends
/// no Wait to the Server.
///
/// The Step's `wait_for` fails with this error. Dex records the failure with the error type
/// `dex_sdk::GracefulCompletionConditionUnsupportedError` and the message of this error, and
/// retries the method according to the Step's retry policy. Upgrade the Dex Server to resolve
/// it.
///
/// # Examples
///
/// ```
/// use dex_sdk::GracefulCompletionConditionUnsupportedError;
///
/// fn describe(error: &GracefulCompletionConditionUnsupportedError) -> String {
///     format!(
///         "upgrade Dex: the graceful completion condition needs protocol {}, the Server has {}",
///         error.required_server_protocol_version(),
///         error.negotiated_server_protocol(),
///     )
/// }
/// ```
///
/// Since 1.45.3.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct GracefulCompletionConditionUnsupportedError {
    negotiated_server_protocol: u32,
    required_server_protocol_version: u32,
}

impl GracefulCompletionConditionUnsupportedError {
    pub(crate) fn new(
        negotiated_server_protocol: u32,
        required_server_protocol_version: u32,
    ) -> Self {
        Self {
            negotiated_server_protocol,
            required_server_protocol_version,
        }
    }

    /// Returns the protocol version agreed with the Server.
    ///
    /// Zero means the Worker has not negotiated a protocol version yet.
    pub fn negotiated_server_protocol(&self) -> u32 {
        self.negotiated_server_protocol
    }

    /// Returns the minimum Server protocol version for the graceful completion condition.
    pub fn required_server_protocol_version(&self) -> u32 {
        self.required_server_protocol_version
    }
}

impl Display for GracefulCompletionConditionUnsupportedError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(
            formatter,
            "dex: the graceful completion condition requires Server protocol {} or later, but the \
             Server negotiated {}",
            self.required_server_protocol_version, self.negotiated_server_protocol
        )
    }
}

impl Error for GracefulCompletionConditionUnsupportedError {}

/// Groups conditions that must all succeed as one branch of [`Wait::any_combination_of`].
pub struct ConditionCombination {
    pub(crate) conditions: Vec<Condition>,
}

impl ConditionCombination {
    /// Creates a branch satisfied only when all `conditions` are satisfied.
    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            conditions: conditions.into_iter().collect(),
        }
    }
}
