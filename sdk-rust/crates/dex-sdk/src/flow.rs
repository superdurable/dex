// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::{
    Context, HandlerResult, PersistenceSchema, RpcList, StepDecision, StepList, Value,
    short_type_name,
};

/// Function registered by a [`Flow`] to handle expiration of its soft timeout.
pub type FlowTimeoutHandler<SomeFlow> = fn(&SomeFlow, &mut Context) -> HandlerResult<StepDecision>;

/// Defines a durable application Flow and its registered API surface.
///
/// A Flow owns a typed start input, Step list, persistence schema, and RPC list. Implementations are
/// registered as owned values in [`crate::Registry`] and shared safely across Worker invocations.
/// Keep [`Self::flow_type`] and all subordinate names stable while any execution is running.
///
/// # Examples
///
/// ```
/// use dex_sdk::{Flow, StepList};
///
/// struct OrderFlow;
///
/// impl Flow for OrderFlow {
///     type StartInput = String;
///
///     fn steps(&self) -> StepList<'_, Self::StartInput> {
///         StepList::empty()
///     }
/// }
/// ```
pub trait Flow: Send + Sync + 'static {
    /// Value accepted by [`crate::Client::start_flow`] and the starting Step.
    type StartInput: Value;

    /// Returns the stable Flow type sent to Dex.
    ///
    /// The default is the final Rust type-name segment.
    fn flow_type(&self) -> &'static str {
        short_type_name::<Self>()
    }

    /// Returns the starting Step and all other Step definitions.
    ///
    /// The default is an empty list, which is valid only for Flows driven entirely by RPCs.
    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }

    /// Declares every Attribute and Channel used by this Flow.
    ///
    /// The default schema is empty.
    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
    }

    /// Returns the optional handler invoked after this Flow's durable timeout timer expires.
    ///
    /// Returning `Some` makes a positive timeout default to [`crate::FlowTimeoutPolicy::Handler`].
    /// One logical execution may retry, and its decision uses normal Step Execute validation.
    fn timeout_handler(&self) -> Option<FlowTimeoutHandler<Self>>
    where
        Self: Sized,
    {
        None
    }

    /// Binds public RPC names to methods on this Flow implementation.
    ///
    /// The default list is empty.
    fn rpcs(&self) -> RpcList<Self>
    where
        Self: Sized,
    {
        RpcList::new()
    }
}
