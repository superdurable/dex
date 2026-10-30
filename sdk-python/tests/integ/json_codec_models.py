# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass, field
from datetime import datetime
from typing import Generic, TypeVar

ResultT = TypeVar("ResultT")


@dataclass
class CodecItem:
    count: int


@dataclass
class CodecReceipt:
    call_id: str = field(metadata={"dex_json_name": "callId"})
    observed_at: datetime = field(
        metadata={"dex_json_name": "observedAt", "dex_json_format": "rfc3339"}
    )


@dataclass
class CodecFailure:
    message: str


@dataclass
class CodecResult(Generic[ResultT]):
    branch: str
    value: ResultT
    receipt: CodecReceipt | None
    failure: CodecFailure | None = None
    attempts: int = field(default=0, metadata={"dex_json_omit_default": True})
    labels: list[str] = field(
        default_factory=list, metadata={"dex_json_omit_default": True}
    )


@dataclass
class CodecEnvelope(Generic[ResultT]):
    results: list[CodecResult[ResultT]]
    indexed: dict[str, CodecResult[ResultT]]
