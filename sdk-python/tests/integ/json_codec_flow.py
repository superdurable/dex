# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Context, Flow, Step, StepDecision, StepList, graceful_complete

from .json_codec_models import CodecEnvelope, CodecItem, CodecReceipt


class JsonCodecStep(Step[CodecEnvelope[CodecItem]]):
    def execute(
        self, context: Context, input: CodecEnvelope[CodecItem]
    ) -> StepDecision:
        result = input.results[0]
        assert type(result.value) is CodecItem
        assert type(result.receipt) is CodecReceipt
        assert result.failure is None
        assert result.attempts == 0
        assert result.labels == []
        assert input.indexed["result"] == result
        return graceful_complete(input)


class JsonCodecFlow(Flow[CodecEnvelope[CodecItem]]):
    start = JsonCodecStep()

    def get_steps(self) -> StepList[CodecEnvelope[CodecItem]]:
        return StepList.start_step(self.start)
