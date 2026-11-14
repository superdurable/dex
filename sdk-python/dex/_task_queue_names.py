# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

MAX_TASK_QUEUE_NAME_LENGTH = 63
RESERVED_TASK_QUEUE_PREFIX = "dex."
DEFAULT_TASK_QUEUE_ALIAS = "dex.default"


def registered_task_queue_name_error(name: str) -> str | None:
    """Check a task queue name that a Worker may register.

    Returns:
        None when ``name`` is valid, including the empty default task queue.
        Otherwise a sentence that explains why the name is invalid.
    """
    if not name:
        return None
    if name.startswith(RESERVED_TASK_QUEUE_PREFIX):
        return 'task queue names starting with "dex." are reserved'
    return _task_queue_characters_error(name)


def target_task_queue_name_error(name: str) -> str | None:
    """Check a task queue name that a started Flow or SubFlow may select.

    Returns:
        None when ``name`` is valid: every registrable name plus
        ``dex.default``. Otherwise a sentence that explains why it is invalid.
    """
    if name == DEFAULT_TASK_QUEUE_ALIAS:
        return None
    return registered_task_queue_name_error(name)


def _task_queue_characters_error(name: str) -> str | None:
    if len(name) > MAX_TASK_QUEUE_NAME_LENGTH:
        return "task queue names have at most 63 characters"
    if not name.isascii() or any(
        not (character.isalnum() or character in "._-") for character in name
    ):
        return "task queue names use only A-Z, a-z, 0-9, '.', '_', and '-'"
    return None
