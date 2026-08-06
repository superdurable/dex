# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.


class PhaseNotImplementedError(RuntimeError):
    """Raised when a contract reaches a later Rust Core phase."""


def require_name(name: str) -> None:
    if not name.strip():
        raise ValueError("durable name is required")


def validate_condition_id(condition_id: str | None) -> None:
    if condition_id is not None and not condition_id:
        raise ValueError("condition ID must not be empty")
