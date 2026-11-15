# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    graceful_complete,
    rpc,
)


class CloneForkWaitStep(Step[str]):
    def __init__(self, label: Attribute[str], release: Channel[str]) -> None:
        self.label = label
        self.release = release

    def wait_for(self, context: Context, input: str) -> Wait:
        self.label.set(context, input)
        return Wait.until(self.release.for_one())

    def execute(self, context: Context, input: str) -> StepDecision:
        answers = self.release.results(context)
        return graceful_complete(f"{self.label.get(context)}|{answers[0]}")


class CloneForkSessionFlow(Flow[str]):
    label = Attribute("clone-fork-label", str)
    release = Channel("clone-fork-release", str)

    def __init__(self) -> None:
        self.wait = CloneForkWaitStep(self.label, self.release)

    def get_flow_type(self) -> str:
        # Other SDK suites may share the test Server; a unique type keeps their Workers apart.
        return "PythonCloneForkSessionFlow"

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.wait)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.label, self.release)

    @rpc
    def release_session(self, context: Context, answer: str) -> None:
        self.release.publish(context, answer)
