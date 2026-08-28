// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList, StepOptions, Wait,
};

static WAIT_VALUE: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("DA_WAIT_UNTIL"));
static EXECUTE_VALUE: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("DA_EXECUTE"));
static BOTH_VALUE: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("DA_BOTH"));

pub(crate) struct StateOptionsWorkflow {
    first: OptionsFirstStep,
    second: OptionsSecondStep,
    third: OptionsThirdStep,
}

impl StateOptionsWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: OptionsFirstStep,
            second: OptionsSecondStep,
            third: OptionsThirdStep,
        }
    }
}

impl Flow for StateOptionsWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first)
            .and(&self.second)
            .and(&self.third)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&WAIT_VALUE)
            .attribute(&EXECUTE_VALUE)
            .attribute(&BOTH_VALUE)
    }
}

struct OptionsFirstStep;

impl Step for OptionsFirstStep {
    type Input = ();

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        EXECUTE_VALUE.set(context, "execute".into())?;
        WAIT_VALUE.set(context, "wait_until".into())?;
        BOTH_VALUE.set(context, "both".into())?;
        Ok(StepDecision::go_to(&OptionsSecondStep, ()))
    }
}

struct OptionsSecondStep;

impl Step for OptionsSecondStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        require_attribute(context, &WAIT_VALUE, "wait_until")?;
        require_attribute(context, &EXECUTE_VALUE, "execute")?;
        require_attribute(context, &BOTH_VALUE, "both")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        require_attribute(context, &EXECUTE_VALUE, "execute")?;
        require_attribute(context, &WAIT_VALUE, "wait_until")?;
        require_attribute(context, &BOTH_VALUE, "both")?;
        Ok(StepDecision::go_to(&OptionsThirdStep, ()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(WAIT_VALUE.lock())
            .execute_lock(EXECUTE_VALUE.lock())
    }
}

struct OptionsThirdStep;

impl Step for OptionsThirdStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        require_attribute(context, &BOTH_VALUE, "both")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        require_attribute(context, &BOTH_VALUE, "both")?;
        Ok(StepDecision::graceful_complete("success".to_string()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(BOTH_VALUE.lock())
            .execute_lock(BOTH_VALUE.lock())
    }
}

fn require_attribute(
    context: &Context,
    attribute: &Attribute<String>,
    expected: &str,
) -> HandlerResult<()> {
    let actual = attribute.get_required(context)?;
    if actual != expected {
        return Err(HandlerError::new(
            "StateOptionsFailure",
            format!("Attribute was {actual}, expected {expected}"),
        ));
    }
    Ok(())
}
