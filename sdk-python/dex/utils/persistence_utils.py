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

from dex.dex_api.models import SearchAttributeValueType, SearchAttribute
from dex.utils.dex_typing import unset_to_none


def get_search_attribute_value(
    sa_type: SearchAttributeValueType, attribute: SearchAttribute
):
    if (
        sa_type == SearchAttributeValueType.KEYWORD
        or sa_type == SearchAttributeValueType.DATETIME
        or sa_type == SearchAttributeValueType.TEXT
    ):
        return unset_to_none(attribute.string_value)
    elif sa_type == SearchAttributeValueType.INT:
        return unset_to_none(attribute.integer_value)
    elif sa_type == SearchAttributeValueType.DOUBLE:
        return unset_to_none(attribute.double_value)
    elif sa_type == SearchAttributeValueType.BOOL:
        return unset_to_none(attribute.bool_value)
    elif sa_type == SearchAttributeValueType.KEYWORD_ARRAY:
        return unset_to_none(attribute.string_array_value)
    else:
        raise ValueError(f"not supported search attribute value type, {sa_type}")
