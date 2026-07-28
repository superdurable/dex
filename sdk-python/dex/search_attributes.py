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

from typing import Union

from dex.errors import WorkflowDefinitionError
from dex.dex_api.models import SearchAttribute, SearchAttributeValueType
from dex.utils.dex_typing import unset_to_none


class SearchAttributes:
    _key_to_type_map: dict[str, SearchAttributeValueType]
    _int64_attribute_map: dict[str, Union[int, None]]
    _upsert_to_server_int64_attribute_map: dict[str, Union[int, None]]
    _string_attribute_map: dict[str, Union[str, None]]
    _upsert_to_server_string_attribute_map: dict[str, Union[str, None]]
    _double_attribute_map: dict[str, Union[float, None]]
    _upsert_to_server_double_attribute_map: dict[str, Union[float, None]]
    _bool_attribute_map: dict[str, Union[bool, None]]
    _upsert_to_server_bool_attribute_map: dict[str, Union[bool, None]]
    _string_array_attribute_map: dict[str, Union[list[str], None]]
    _upsert_to_server_string_array_attribute_map: dict[str, Union[list[str], None]]

    def __init__(
        self,
        key_to_type_map: dict[str, SearchAttributeValueType],
        search_attribute_map: Union[list[SearchAttribute], None],
    ):
        self._key_to_type_map = key_to_type_map
        self._int64_attribute_map = {}
        self._upsert_to_server_int64_attribute_map = {}
        self._string_attribute_map = {}
        self._upsert_to_server_string_attribute_map = {}
        self._double_attribute_map = {}
        self._upsert_to_server_double_attribute_map = {}
        self._bool_attribute_map = {}
        self._upsert_to_server_bool_attribute_map = {}
        self._string_array_attribute_map = {}
        self._upsert_to_server_string_array_attribute_map = {}

        if search_attribute_map is not None:
            for attribute in search_attribute_map:
                key = unset_to_none(attribute.key)
                if key is None:
                    raise ValueError("empty search attribute value type is not allowed")
                value_type = key_to_type_map[key]

                if (
                    value_type == SearchAttributeValueType.KEYWORD
                    or value_type == SearchAttributeValueType.DATETIME
                    or value_type == SearchAttributeValueType.TEXT
                ):
                    self._string_attribute_map[key] = unset_to_none(
                        attribute.string_value
                    )
                elif value_type == SearchAttributeValueType.INT:
                    self._int64_attribute_map[key] = unset_to_none(
                        attribute.integer_value
                    )
                elif value_type == SearchAttributeValueType.DOUBLE:
                    self._double_attribute_map[key] = unset_to_none(
                        attribute.double_value
                    )
                elif value_type == SearchAttributeValueType.BOOL:
                    self._bool_attribute_map[key] = unset_to_none(attribute.bool_value)
                elif value_type == SearchAttributeValueType.KEYWORD_ARRAY:
                    self._string_array_attribute_map[key] = unset_to_none(
                        attribute.string_array_value
                    )
                else:
                    raise ValueError(
                        f"not supported search attribute value type, {value_type}"
                    )

    def get_search_attribute_int64(self, key: str) -> Union[int, None]:
        return self._int64_attribute_map.get(key)

    def set_search_attribute_int64(self, key: str, value: Union[int, None]):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.INT
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as int64")
        self._int64_attribute_map[key] = value
        self._upsert_to_server_int64_attribute_map[key] = value

    def get_search_attribute_double(self, key: str) -> Union[float, None]:
        return self._double_attribute_map.get(key)

    def set_search_attribute_double(self, key: str, value: Union[float, None]):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.DOUBLE
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as double")
        self._double_attribute_map[key] = value
        self._upsert_to_server_double_attribute_map[key] = value

    def get_search_attribute_boolean(self, key: str) -> Union[bool, None]:
        return self._bool_attribute_map.get(key)

    def set_search_attribute_boolean(self, key: str, value: Union[bool, None]):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.BOOL
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as bool")
        self._bool_attribute_map[key] = value
        self._upsert_to_server_bool_attribute_map[key] = value

    def get_search_attribute_keyword(self, key: str) -> Union[str, None]:
        return self._string_attribute_map.get(key)

    def set_search_attribute_keyword(self, key: str, value: Union[str, None]):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.KEYWORD
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as keyword")
        self._string_attribute_map[key] = value
        self._upsert_to_server_string_attribute_map[key] = value

    def get_search_attribute_text(self, key: str) -> Union[str, None]:
        return self._string_attribute_map.get(key)

    def set_search_attribute_text(self, key: str, value: Union[str, None]):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.TEXT
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as text")
        self._string_attribute_map[key] = value
        self._upsert_to_server_string_attribute_map[key] = value

    def get_search_attribute_datetime(self, key: str) -> Union[str, None]:
        return self._string_attribute_map.get(key)

    def set_search_attribute_datetime(self, key: str, value: Union[str, None]):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.DATETIME
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as datetime")
        self._string_attribute_map[key] = value
        self._upsert_to_server_string_attribute_map[key] = value

    def get_search_attribute_keyword_array(self, key: str) -> Union[list[str], None]:
        return self._string_array_attribute_map.get(key)

    def set_search_attribute_keyword_array(
        self, key: str, value: Union[list[str], None]
    ):
        if (
            key not in self._key_to_type_map
            or self._key_to_type_map[key] != SearchAttributeValueType.KEYWORD_ARRAY
        ):
            raise WorkflowDefinitionError(f"key {key} is not defined as keyword array")
        self._string_array_attribute_map[key] = value
        self._upsert_to_server_string_array_attribute_map[key] = value

    def get_upsert_to_server_int64_attribute_map(self) -> dict[str, Union[int, None]]:
        return self._upsert_to_server_int64_attribute_map

    def get_upsert_to_server_string_attribute_map(self) -> dict[str, Union[str, None]]:
        return self._upsert_to_server_string_attribute_map

    def get_upsert_to_server_string_array_attribute_map(
        self,
    ) -> dict[str, Union[list[str], None]]:
        return self._upsert_to_server_string_array_attribute_map

    def get_upsert_to_server_bool_attribute_map(self) -> dict[str, Union[bool, None]]:
        return self._upsert_to_server_bool_attribute_map

    def get_upsert_to_server_double_attribute_map(
        self,
    ) -> dict[str, Union[float, None]]:
        return self._upsert_to_server_double_attribute_map
