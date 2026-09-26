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

from __future__ import annotations

from dataclasses import dataclass

from dex import AttributeMap, Context, Flow, PersistenceSchema, RPCResult, StepList, rpc

FLOW_ID = "customer-directory"
PARTITION_COUNT = 1000
FNV_OFFSET_BASIS_32 = 2166136261
FNV_PRIME_32 = 16777619


@dataclass
class CustomerProfile:
    email_address: str
    full_name: str
    company_name: str
    customer_tier: str


@dataclass
class CustomerProfilePartition:
    profiles_by_canonical_email: dict[str, CustomerProfile]


class CustomerDirectoryFlow(Flow[None]):
    customer_profiles_by_email_partition = AttributeMap(
        "customer_profiles_by_email_partition",
        CustomerProfilePartition,
    )

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.customer_profiles_by_email_partition)

    @rpc(name="UpsertCustomerProfile")
    def upsert_customer_profile(
        self,
        context: Context,
        input: CustomerProfile,
    ) -> RPCResult[CustomerProfile]:
        canonical_email, partition_name, _ = email_partition(input.email_address)
        input.email_address = canonical_email
        try:
            partition = self.customer_profiles_by_email_partition.get(
                context, partition_name
            )
        except KeyError:
            partition = CustomerProfilePartition({})
        partition.profiles_by_canonical_email[canonical_email] = input
        self.customer_profiles_by_email_partition.set(
            context, partition_name, partition
        )
        return RPCResult(input)

    @rpc(name="GetCustomerProfileByEmail")
    def get_customer_profile_by_email(
        self,
        context: Context,
        input: str,
    ) -> RPCResult[CustomerProfile]:
        canonical_email, partition_name, _ = email_partition(input)
        try:
            partition = self.customer_profiles_by_email_partition.get(
                context, partition_name
            )
            profile = partition.profiles_by_canonical_email[canonical_email]
        except KeyError as error:
            raise ValueError(
                f'customer profile "{canonical_email}" not found'
            ) from error
        return RPCResult(profile)


def email_partition(email_address: str) -> tuple[str, str, int]:
    canonical_email = canonical_email_address(email_address)
    hash_value = fnv1a_32(canonical_email.encode("ascii"))
    return (
        canonical_email,
        f"partition-{hash_value % PARTITION_COUNT:03d}",
        hash_value,
    )


def canonical_email_address(email_address: str) -> str:
    try:
        encoded = email_address.encode("ascii")
    except UnicodeEncodeError as error:
        raise ValueError("emailAddress must contain only ASCII characters") from error
    start = 0
    while start < len(encoded) and _is_ascii_whitespace(encoded[start]):
        start += 1
    end = len(encoded)
    while end > start and _is_ascii_whitespace(encoded[end - 1]):
        end -= 1
    if start == end:
        raise ValueError("emailAddress is required")
    canonical = bytearray(encoded[start:end])
    for index, value in enumerate(canonical):
        if ord("A") <= value <= ord("Z"):
            canonical[index] = value + ord("a") - ord("A")
    return canonical.decode("ascii")


def fnv1a_32(value: bytes) -> int:
    hash_value = FNV_OFFSET_BASIS_32
    for octet in value:
        hash_value ^= octet
        hash_value = (hash_value * FNV_PRIME_32) & 0xFFFFFFFF
    return hash_value


def _is_ascii_whitespace(value: int) -> bool:
    return value == ord(" ") or ord("\t") <= value <= ord("\r")
