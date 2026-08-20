// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList, StepOptions, Wait,
};

pub(crate) struct StateOptionsWorkflow {
    pub(crate) wait_value: Attribute<String>,
    pub(crate) execute_value: Attribute<String>,
    pub(crate) both_value: Attribute<String>,
    first: OptionsFirstStep,
    second: OptionsSecondStep,
    third: OptionsThirdStep,
}

impl StateOptionsWorkflow {
    pub(crate) fn new() -> Self {
        let wait_value = Attribute::new("DA_WAIT_UNTIL");
        let execute_value = Attribute::new("DA_EXECUTE");
        let both_value = Attribute::new("DA_BOTH");
        Self {
            first: OptionsFirstStep {
                wait_value: wait_value.clone(),
                execute_value: execute_value.clone(),
                both_value: both_value.clone(),
            },
            second: OptionsSecondStep {
                wait_value: wait_value.clone(),
                execute_value: execute_value.clone(),
                both_value: both_value.clone(),
            },
            third: OptionsThirdStep {
                both_value: both_value.clone(),
            },
            wait_value,
            execute_value,
            both_value,
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
            .attribute(&self.wait_value)
            .attribute(&self.execute_value)
            .attribute(&self.both_value)
    }
}

struct OptionsFirstStep {
    wait_value: Attribute<String>,
    execute_value: Attribute<String>,
    both_value: Attribute<String>,
}

impl Step for OptionsFirstStep {
    type Input = ();

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        self.execute_value.set(context, "execute".into())?;
        self.wait_value.set(context, "wait_until".into())?;
        self.both_value.set(context, "both".into())?;
        Ok(StepDecision::go_to(
            &OptionsSecondStep {
                wait_value: self.wait_value.clone(),
                execute_value: self.execute_value.clone(),
                both_value: self.both_value.clone(),
            },
            (),
        ))
    }
}

struct OptionsSecondStep {
    wait_value: Attribute<String>,
    execute_value: Attribute<String>,
    both_value: Attribute<String>,
}

impl Step for OptionsSecondStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        require_attribute(context, &self.wait_value, "wait_until")?;
        require_attribute(context, &self.execute_value, "execute")?;
        require_attribute(context, &self.both_value, "both")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        require_attribute(context, &self.execute_value, "execute")?;
        require_attribute(context, &self.wait_value, "wait_until")?;
        require_attribute(context, &self.both_value, "both")?;
        Ok(StepDecision::go_to(
            &OptionsThirdStep {
                both_value: self.both_value.clone(),
            },
            (),
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(self.wait_value.lock())
            .execute_lock(self.execute_value.lock())
    }
}

struct OptionsThirdStep {
    both_value: Attribute<String>,
}

impl Step for OptionsThirdStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        require_attribute(context, &self.both_value, "both")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        require_attribute(context, &self.both_value, "both")?;
        Ok(StepDecision::graceful_complete("success".to_string()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(self.both_value.lock())
            .execute_lock(self.both_value.lock())
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
