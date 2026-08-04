# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from typing import Optional, TypeVar, Union

from dex.dex_api.types import Unset

T = TypeVar("T")

def unset_to_none(input: Union[Unset, T]) -> Optional[T]:
    if isinstance(input, Unset):
        return None
    return input

def assert_not_unset(input: Union[Unset, T]) -> T:
    if isinstance(input, Unset):
        raise RuntimeError("the value shouldn't be Unset")
    return input
