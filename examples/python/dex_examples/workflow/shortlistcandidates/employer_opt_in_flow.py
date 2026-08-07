# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from dex import (
    Attribute,
    AttributeIndex,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    dead_end,
    force_complete,
    rpc,
)

from dex_examples.workflow.shortlistcandidates.employer_opt_in_input import (
    EmployerOptInInput,
)

DA_EMPLOYER_ID = "EMPLOYER_OPT_IN_EmployerId"
DA_OPTED_IN = "EMPLOYER_OPT_IN_Status"


class OptOut(Step[None]):
    def __init__(self, opted_in: Attribute[bool]) -> None:
        self.opted_in = opted_in

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.opted_in.set(context, False)
        return force_complete()


class OptIn(Step[EmployerOptInInput]):
    def __init__(
        self,
        employer_id: Attribute[str],
        opted_in: Attribute[bool],
    ) -> None:
        self.employer_id = employer_id
        self.opted_in = opted_in

    def execute(self, context: Context, input: EmployerOptInInput) -> StepDecision:
        self.employer_id.set(context, input.employer_id)
        self.opted_in.set(context, True)
        return dead_end()


class EmployerOptInFlow(Flow[EmployerOptInInput]):
    employer_id = Attribute(
        DA_EMPLOYER_ID,
        str,
        AttributeIndex(IndexType.KEYWORD, "CustomKeywordField"),
    )
    opted_in = Attribute(DA_OPTED_IN, bool)

    def __init__(self) -> None:
        self.opt_in = OptIn(self.employer_id, self.opted_in)
        self.opt_out_step = OptOut(self.opted_in)

    def get_steps(self) -> StepList[EmployerOptInInput]:
        return StepList.start_step(self.opt_in).other_steps(self.opt_out_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.employer_id, self.opted_in)

    @rpc
    def is_opted_in(self, context: Context) -> RPCResult[bool]:
        return RPCResult(self.opted_in.get(context))

    @rpc
    def opt_out(self, context: Context) -> RPCResult[None]:
        del context
        return RPCResult(
            None,
            next_steps=(StepMovement.of(self.opt_out_step, None),),
        )
