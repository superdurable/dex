# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

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
