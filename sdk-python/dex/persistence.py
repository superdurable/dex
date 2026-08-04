# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from typing import Any, Tuple, Union

from dex.data_attributes import DataAttributes
from dex.search_attributes import SearchAttributes
from dex.state_execution_locals import StateExecutionLocals

class Persistence:
    _data_attributes: DataAttributes
    _search_attributes: SearchAttributes
    _state_execution_locals: StateExecutionLocals

    def __init__(
        self,
        data_attributes: DataAttributes,
        search_attributes: SearchAttributes,
        state_execution_locals: StateExecutionLocals,
    ):
        self._data_attributes = data_attributes
        self._search_attributes = search_attributes
        self._state_execution_locals = state_execution_locals

    def get_data_attribute(self, key: str) -> Any:
        return self._data_attributes.get_data_attribute(key)

    def set_data_attribute(self, key: str, value: Any):
        self._data_attributes.set_data_attribute(key, value)

    def get_search_attribute_int64(self, key: str) -> Union[None, int]:
        return self._search_attributes.get_search_attribute_int64(key)

    def set_search_attribute_int64(self, key: str, value: Union[None, int]):
        self._search_attributes.set_search_attribute_int64(key, value)

    def get_search_attribute_double(self, key: str) -> Union[None, float]:
        return self._search_attributes.get_search_attribute_double(key)

    def set_search_attribute_double(self, key: str, value: Union[None, float]):
        self._search_attributes.set_search_attribute_double(key, value)

    def get_search_attribute_boolean(self, key: str) -> Union[None, bool]:
        return self._search_attributes.get_search_attribute_boolean(key)

    def set_search_attribute_boolean(self, key: str, value: Union[None, bool]):
        self._search_attributes.set_search_attribute_boolean(key, value)

    def get_search_attribute_keyword(self, key: str) -> Union[None, str]:
        return self._search_attributes.get_search_attribute_keyword(key)

    def set_search_attribute_keyword(self, key: str, value: Union[None, str]):
        self._search_attributes.set_search_attribute_keyword(key, value)

    def get_search_attribute_text(self, key: str) -> Union[None, str]:
        return self._search_attributes.get_search_attribute_text(key)

    def set_search_attribute_text(self, key: str, value: Union[None, str]):
        self._search_attributes.set_search_attribute_text(key, value)

    def get_search_attribute_datetime(self, key: str) -> Union[None, str]:
        return self._search_attributes.get_search_attribute_datetime(key)

    def set_search_attribute_datetime(self, key: str, value: Union[None, str]):
        self._search_attributes.set_search_attribute_datetime(key, value)

    def get_search_attribute_keyword_array(self, key: str) -> Union[None, list[str]]:
        return self._search_attributes.get_search_attribute_keyword_array(key)

    def set_search_attribute_keyword_array(
        self, key: str, value: Union[None, list[str]]
    ):
        self._search_attributes.set_search_attribute_keyword_array(key, value)

    def get_state_execution_local(self, key: str) -> Any:
        return self._state_execution_locals.get_state_execution_local(key)

    def set_state_execution_local(self, key: str, value: Any):
        self._state_execution_locals.set_state_execution_local(key, value)

    def record_event(self, key: str, *event_data: Tuple[Any, ...]):
        self._state_execution_locals.record_event(key, *event_data)
