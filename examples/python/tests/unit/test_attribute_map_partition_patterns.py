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

import pytest

from dex_examples.patterns.hash_partitioned_attribute_map.customer_directory_flow import (
    canonical_email_address,
    email_partition,
)
from dex_examples.patterns.sequentially_chunked_attribute_map.chunked_subscriber_flow import (
    CURRENT_CHUNK_INSTANCE,
    validate_subscriber_page_token,
)


@pytest.mark.parametrize(
    ("email_address", "canonical_email", "hash_value", "partition_name"),
    [
        (" Alice@Example.COM ", "alice@example.com", 2493822278, "partition-278"),
        ("bob@example.com", "bob@example.com", 3055529145, "partition-145"),
        (
            "support+west@example.org",
            "support+west@example.org",
            2156001632,
            "partition-632",
        ),
    ],
)
def test_email_partition_golden_vectors(
    email_address: str,
    canonical_email: str,
    hash_value: int,
    partition_name: str,
) -> None:
    assert email_partition(email_address) == (
        canonical_email,
        partition_name,
        hash_value,
    )


@pytest.mark.parametrize("email_address", ["", " \t\r\n", "josé@example.com"])
def test_canonical_email_rejects_invalid_input(email_address: str) -> None:
    with pytest.raises(ValueError):
        canonical_email_address(email_address)


def test_subscriber_page_tokens() -> None:
    assert validate_subscriber_page_token("") == CURRENT_CHUNK_INSTANCE
    assert validate_subscriber_page_token("00000000000000000101") == (
        "00000000000000000101"
    )
    for page_token in ("1", "archive", "00000000000000000002"):
        with pytest.raises(ValueError):
            validate_subscriber_page_token(page_token)
