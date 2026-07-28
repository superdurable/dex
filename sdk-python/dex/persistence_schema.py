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
from typing import List, Optional

from dex.dex_api.models import SearchAttributeValueType


class PersistenceFieldType(Enum):
    DataAttribute = 1
    SearchAttribute = 2
    DataAttributePrefix = 3


@dataclass
class PersistenceField:
    key: str
    field_type: PersistenceFieldType
    value_type: Optional[type]
    search_attribute_type: Optional[SearchAttributeValueType] = None

    @classmethod
    def data_attribute_def(cls, key: str, value_type: Optional[type]):
        return PersistenceField(key, PersistenceFieldType.DataAttribute, value_type)

    @classmethod
    def search_attribute_def(cls, key: str, sa_type: SearchAttributeValueType):
        return PersistenceField(
            key, PersistenceFieldType.SearchAttribute, None, sa_type
        )

    @classmethod
    def data_attribute_prefix_def(cls, key: str, value_type: Optional[type]):
        return PersistenceField(
            key, PersistenceFieldType.DataAttributePrefix, value_type
        )


@dataclass
class PersistenceSchema:
    persistence_fields: List[PersistenceField] = field(default_factory=list)

    @classmethod
    def create(cls, *args: PersistenceField):
        return PersistenceSchema(list(args))
