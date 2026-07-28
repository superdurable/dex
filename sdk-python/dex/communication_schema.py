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

from dataclasses import dataclass, field
from enum import Enum
from typing import List, Optional, Union


class CommunicationMethodType(Enum):
    SignalChannel = 1
    InternalChannel = 2


@dataclass
class CommunicationMethod:
    name: str
    method_type: CommunicationMethodType
    value_type: Optional[type]
    is_prefix: bool

    @classmethod
    def signal_channel_def(cls, name: str, value_type: Union[type, None]):
        return CommunicationMethod(
            name,
            CommunicationMethodType.SignalChannel,
            value_type if value_type is not None else type(None),
            False,
        )

    @classmethod
    def internal_channel_def(cls, name: str, value_type: Union[type, None]):
        return CommunicationMethod(
            name,
            CommunicationMethodType.InternalChannel,
            value_type if value_type is not None else type(None),
            False,
        )

    @classmethod
    def internal_channel_def_by_prefix(
        cls, name_prefix: str, value_type: Union[type, None]
    ):
        return CommunicationMethod(
            name_prefix,
            CommunicationMethodType.InternalChannel,
            value_type if value_type is not None else type(None),
            True,
        )


@dataclass
class CommunicationSchema:
    communication_methods: List[CommunicationMethod] = field(default_factory=list)

    @classmethod
    def create(cls, *methods: CommunicationMethod):
        return CommunicationSchema(list(methods))
