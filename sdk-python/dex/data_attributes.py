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

from typing import Any, Union

from dex.errors import WorkflowDefinitionError
from dex.dex_api.models import EncodedObject
from dex.dex_api.types import Unset
from dex.object_encoder import ObjectEncoder
from dex.type_store import TypeStore


class DataAttributes:
    _type_store: TypeStore
    _object_encoder: ObjectEncoder
    _current_values: dict[str, Union[EncodedObject, None, Unset]]
    _updated_values_to_return: dict[str, Union[EncodedObject, Unset]]

    def __init__(
        self,
        type_store: TypeStore,
        object_encoder: ObjectEncoder,
        current_values: dict[str, Union[EncodedObject, None, Unset]],
    ):
        self._object_encoder = object_encoder
        self._type_store = type_store
        self._current_values = current_values
        self._updated_values_to_return = {}

    def get_data_attribute(self, key: str) -> Any:
        is_registered = self._type_store.is_valid_name_or_prefix(key)
        if not is_registered:
            raise WorkflowDefinitionError(f"data attribute %s is not registered {key}")

        encoded_object = self._current_values.get(key)
        if encoded_object is None:
            return None

        registered_type = self._type_store.get_type(key)
        return self._object_encoder.decode(encoded_object, registered_type)

    def set_data_attribute(self, key: str, value: Any):
        is_registered = self._type_store.is_valid_name_or_prefix(key)
        if not is_registered:
            raise WorkflowDefinitionError(
                f"data attribute {key} is not registered {key}"
            )

        registered_type = self._type_store.get_type(key)
        if (
            value is not None
            and registered_type is not None
            and not isinstance(value, registered_type)
        ):
            raise WorkflowDefinitionError(
                f"data attribute {key} is of the right type {registered_type}"
            )

        encoded_value = self._object_encoder.encode(value)
        self._current_values[key] = encoded_value
        self._updated_values_to_return[key] = encoded_value

    def get_updated_values_to_return(self) -> dict[str, Union[EncodedObject, Unset]]:
        return self._updated_values_to_return
