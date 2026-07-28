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

from typing import Optional
from enum import Enum

from dex.communication_schema import CommunicationMethod
from dex.errors import WorkflowDefinitionError, NotRegisteredError
from dex.persistence_schema import PersistenceField, PersistenceFieldType


class Type(Enum):
    INTERNAL_CHANNEL = 1
    DATA_ATTRIBUTE = 2
    # TODO: extend to other types
    # SIGNAL_CHANNEL = 3


class TypeStore:
    _class_type: Type
    _name_to_type_store: dict[str, Optional[type]]
    _prefix_to_type_store: dict[str, Optional[type]]

    def __init__(self, class_type: Type):
        self._class_type = class_type
        self._name_to_type_store = dict()
        self._prefix_to_type_store = dict()

    def is_valid_name_or_prefix(self, name: str) -> bool:
        return self._validate_name(name)

    def get_type(self, name: str) -> type:
        is_registered = self._validate_name(name)

        if not is_registered:
            raise NotRegisteredError(f"{self._class_type} not registered: {name}")

        t = self._do_get_type(name)
        if t is None:
            raise NotRegisteredError(f"{self._class_type} not registered: {name}")

        return t

    def add_internal_channel_def(self, obj: CommunicationMethod):
        if self._class_type != Type.INTERNAL_CHANNEL:
            raise ValueError(
                f"Cannot add internal channel definition to {self._class_type}"
            )
        self._do_add_to_store(obj.is_prefix, obj.name, obj.value_type)

    def add_data_attribute_def(self, obj: PersistenceField):
        if self._class_type != Type.DATA_ATTRIBUTE:
            raise ValueError(
                f"Cannot add internal channel definition to {self._class_type}"
            )
        self._do_add_to_store(
            obj.field_type == PersistenceFieldType.DataAttributePrefix,
            obj.key,
            obj.value_type,
        )

    def _validate_name(self, name: str) -> bool:
        if name in self._name_to_type_store:
            return True

        for prefix in self._prefix_to_type_store.keys():
            if name.startswith(prefix):
                return True

        return False

    def _do_get_type(self, name: str) -> Optional[type]:
        if name in self._name_to_type_store:
            t = self._name_to_type_store[name]
            return t if t is not None else type(None)

        for prefix, t in self._prefix_to_type_store.items():
            if name.startswith(prefix):
                return t if t is not None else type(None)

        return None

    def _do_add_to_store(self, is_prefix: bool, name: str, t: Optional[type]):
        if is_prefix:
            store = self._prefix_to_type_store
        else:
            store = self._name_to_type_store

        if name in store:
            raise WorkflowDefinitionError(
                f"{self._class_type} name/prefix {name} already exists"
            )

        store[name] = t
