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

from typing import Any, List, Tuple, Union

from dex.errors import WorkflowDefinitionError
from dex.dex_api.models import EncodedObject, KeyValue
from dex.dex_api.types import Unset
from dex.object_encoder import ObjectEncoder


class StateExecutionLocals:
    _record_events: dict[str, Union[EncodedObject, Unset]]
    _attribute_name_to_encoded_object_map: dict[str, Union[EncodedObject, Unset]]
    _upsert_attributes_to_return_to_server: dict[str, Union[EncodedObject, Unset]]
    _object_encoder: ObjectEncoder

    def __init__(
        self,
        attribute_name_to_encoded_object_map: dict[str, Union[EncodedObject, Unset]],
        object_encoder: ObjectEncoder,
    ):
        self._object_encoder = object_encoder
        self._attribute_name_to_encoded_object_map = (
            attribute_name_to_encoded_object_map
        )
        self._upsert_attributes_to_return_to_server = {}
        self._record_events = {}

    def set_state_execution_local(self, key: str, value: Any):
        encoded_data = self._object_encoder.encode(value)
        self._attribute_name_to_encoded_object_map[key] = encoded_data
        self._upsert_attributes_to_return_to_server[key] = encoded_data

    def get_state_execution_local(self, key: str) -> Any:
        encoded_object = self._attribute_name_to_encoded_object_map.get(key)
        if encoded_object is None:
            return None
        return self._object_encoder.decode(encoded_object)

    def record_event(self, key: str, *event_data: Tuple[Any, ...]):
        if key in self._record_events:
            raise WorkflowDefinitionError("Cannot record the same event more than once")

        if event_data is not None and len(event_data) == 1:
            self._record_events[key] = self._object_encoder.encode(event_data[0])

        self._record_events[key] = self._object_encoder.encode(event_data)

    def get_upsert_state_execution_local_attributes(self) -> List[KeyValue]:
        return [
            KeyValue(item_key, item_value)
            for item_key, item_value in self._upsert_attributes_to_return_to_server.items()
        ]

    def get_record_events(self) -> List[KeyValue]:
        return [
            KeyValue(item_key, item_value)
            for item_key, item_value in self._record_events.items()
        ]
