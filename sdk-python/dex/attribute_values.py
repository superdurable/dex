# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from collections.abc import Mapping
from types import MappingProxyType
from typing import Any, TypeVar, overload

from dex.attribute import Attribute, AttributeMapInstance
from dex.flow import Registry

ValueT = TypeVar("ValueT")
DefaultT = TypeVar("DefaultT")


class AttributeValues:
    """Hold the typed values returned by one ``get_attributes`` call.

    ``Client.get_attributes`` and ``AsyncClient.get_attributes`` return an
    AttributeValues. Look up each value with the ``Attribute`` or
    ``AttributeMapInstance`` that was passed to the call. An equal definition or
    selector finds the same value. Type checkers infer the value type from the
    key, so ``values[ORDER_STATUS]`` has the Attribute's value type.

    A read is set when the Flow run stores a value for it. A stored value that
    decodes to ``None`` is still set. A read is unset when the run stores no
    value for it. A key that was not part of the call behaves like an unset
    read:

    * ``values[key]`` returns the value of a set read and raises ``KeyError``
      otherwise.
    * ``values.get(key)`` returns the value of a set read and ``None``
      otherwise.
    * ``values.get(key, default)`` returns the value of a set read and
      ``default`` otherwise.
    * ``key in values`` is ``True`` only for a set read.

    AttributeValues is read-only and not iterable. Keep the reads you passed to
    look up their values. Its representation and ``KeyError`` messages name each
    read by its physical key, such as ``notes/n1``, but never show values, so they
    are safe to log for hard-delete Attributes. ``AttributeValues()`` creates an
    empty result in which every read is unset.

    Example::

        ORDER_STATUS = Attribute("order-status", str)
        NOTES = AttributeMap("notes", Note)

        values = client.get_attributes("order-42", ORDER_STATUS, NOTES.instance("n1"))
        status = values.get(ORDER_STATUS, "new")
        if NOTES.instance("n1") in values:
            print(values[NOTES.instance("n1")].text)
    """

    __slots__ = ("_values",)
    __iter__ = None

    def __init__(self) -> None:
        """Create an empty result in which every read is unset."""
        self._values: Mapping[Attribute[Any] | AttributeMapInstance[Any], object] = (
            MappingProxyType({})
        )

    @classmethod
    def _of_set_values(
        cls,
        values: dict[Attribute[Any] | AttributeMapInstance[Any], object],
    ) -> AttributeValues:
        result = cls()
        result._values = MappingProxyType(values)
        return result

    @overload
    def __getitem__(self, read: Attribute[ValueT]) -> ValueT: ...

    @overload
    def __getitem__(self, read: AttributeMapInstance[ValueT]) -> ValueT: ...

    def __getitem__(self, read: Attribute[Any] | AttributeMapInstance[Any]) -> Any:
        """Return the value of a set read.

        Args:
            read: The Attribute or AttributeMapInstance passed to ``get_attributes``,
                or an equal one.

        Returns:
            The decoded value. It can be ``None`` when the stored value decodes to
            ``None``.

        Raises:
            KeyError: If the read is unset or was not part of the call.
        """
        try:
            return self._values[read]
        except KeyError:
            raise KeyError(_describe_read(read)) from None

    @overload
    def get(self, read: Attribute[ValueT]) -> ValueT | None: ...

    @overload
    def get(self, read: AttributeMapInstance[ValueT]) -> ValueT | None: ...

    @overload
    def get(self, read: Attribute[ValueT], default: DefaultT) -> ValueT | DefaultT: ...

    @overload
    def get(
        self, read: AttributeMapInstance[ValueT], default: DefaultT
    ) -> ValueT | DefaultT: ...

    def get(
        self,
        read: Attribute[Any] | AttributeMapInstance[Any],
        default: object = None,
    ) -> Any:
        """Return the value of a read, or a default when it is unset.

        Use ``get`` when an unset Attribute has an application default. Use
        ``key in values`` instead when a stored ``None`` must be told apart from an
        unset read.

        Args:
            read: The Attribute or AttributeMapInstance passed to ``get_attributes``,
                or an equal one.
            default: The value returned when the read is unset or was not part of
                the call. Defaults to ``None``.

        Returns:
            The decoded value of a set read, or ``default``.
        """
        return self._values.get(read, default)

    def __contains__(self, read: object) -> bool:
        """Report whether a read is set.

        Args:
            read: The Attribute or AttributeMapInstance passed to ``get_attributes``,
                or an equal one.

        Returns:
            ``True`` when the call read this key and found a stored value, including
            a value that decodes to ``None``. ``False`` when the read is unset, was
            not part of the call, or is not an Attribute read.
        """
        if not isinstance(read, (Attribute, AttributeMapInstance)):
            return False
        return read in self._values

    def __repr__(self) -> str:
        """Return the physical keys of the set reads without their values.

        Returns:
            A representation that lists the physical key of every set read.
        """
        names = ", ".join(_describe_read(read) for read in self._values)
        return f"AttributeValues(set=[{names}])"


def _describe_read(read: object) -> str:
    if isinstance(read, AttributeMapInstance):
        return repr(Registry.physical_name(read.attribute_map.name, read.instance))
    if isinstance(read, Attribute):
        return repr(read.name)
    return repr(read)
