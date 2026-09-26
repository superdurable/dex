/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import {
  AttributeMap,
  StepList,
  jsonCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "@superdurable/dex";

export const CUSTOMER_DIRECTORY_FLOW_ID = "customer-directory";
export const CUSTOMER_PARTITION_COUNT = 1000;
const FNV_OFFSET_BASIS_32 = 2166136261;
const FNV_PRIME_32 = 16777619;

export interface CustomerProfile {
  readonly emailAddress: string;
  readonly fullName: string;
  readonly companyName: string;
  readonly customerTier: string;
}

export interface CustomerProfilePartition {
  readonly profilesByCanonicalEmail: Readonly<Record<string, CustomerProfile>>;
}

const customerProfileCodec = jsonCodec<CustomerProfile>();
const customerProfilePartitionCodec = jsonCodec<CustomerProfilePartition>();
const customerProfilesByEmailPartition = new AttributeMap(
  "customer_profiles_by_email_partition",
  customerProfilePartitionCodec,
);

export class CustomerDirectoryFlow implements Flow<void> {
  public readonly customerProfilesByEmailPartition = customerProfilesByEmailPartition;

  public getFlowType(): string {
    return "CustomerDirectoryFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.customerProfilesByEmailPartition] };
  }

  @rpc({
    name: "UpsertCustomerProfile",
    inputCodec: customerProfileCodec,
    outputCodec: customerProfileCodec,
  })
  public upsertCustomerProfile(
    context: Context,
    input: CustomerProfile,
  ): RPCResult<CustomerProfile> {
    const { canonicalEmail, partitionName } = emailPartition(input.emailAddress);
    const profile = { ...input, emailAddress: canonicalEmail };
    const partition = this.customerProfilesByEmailPartition.get(context, partitionName);
    this.customerProfilesByEmailPartition.set(context, partitionName, {
      profilesByCanonicalEmail: {
        ...(partition?.profilesByCanonicalEmail ?? {}),
        [canonicalEmail]: profile,
      },
    });
    return { output: profile };
  }

  @rpc({
    name: "GetCustomerProfileByEmail",
    inputCodec: stringCodec,
    outputCodec: customerProfileCodec,
  })
  public getCustomerProfileByEmail(
    context: Context,
    input: string,
  ): RPCResult<CustomerProfile> {
    const { canonicalEmail, partitionName } = emailPartition(input);
    const partition = this.customerProfilesByEmailPartition.get(context, partitionName);
    const profile = partition?.profilesByCanonicalEmail[canonicalEmail];
    if (profile === undefined) {
      throw new Error(`customer profile "${canonicalEmail}" not found`);
    }
    return { output: profile };
  }
}

export interface EmailPartition {
  readonly canonicalEmail: string;
  readonly hash: number;
  readonly partitionName: string;
}

export function emailPartition(emailAddress: string): EmailPartition {
  const canonicalEmail = canonicalEmailAddress(emailAddress);
  const hash = fnv1a32(Buffer.from(canonicalEmail, "ascii"));
  return {
    canonicalEmail,
    hash,
    partitionName: `partition-${String(hash % CUSTOMER_PARTITION_COUNT).padStart(3, "0")}`,
  };
}

export function canonicalEmailAddress(emailAddress: string): string {
  for (const character of emailAddress) {
    if (character.charCodeAt(0) > 0x7f) {
      throw new Error("emailAddress must contain only ASCII characters");
    }
  }
  let start = 0;
  while (start < emailAddress.length && isASCIIWhitespace(emailAddress.charCodeAt(start))) {
    start += 1;
  }
  let end = emailAddress.length;
  while (end > start && isASCIIWhitespace(emailAddress.charCodeAt(end - 1))) {
    end -= 1;
  }
  if (start === end) {
    throw new Error("emailAddress is required");
  }
  return emailAddress.slice(start, end).replaceAll(/[A-Z]/g, (character) =>
    character.toLowerCase(),
  );
}

export function fnv1a32(value: Uint8Array): number {
  let hash = FNV_OFFSET_BASIS_32;
  for (const octet of value) {
    hash = Math.imul(hash ^ octet, FNV_PRIME_32) >>> 0;
  }
  return hash;
}

function isASCIIWhitespace(value: number): boolean {
  return value === 0x20 || (value >= 0x09 && value <= 0x0d);
}

export const customerDirectoryFlow = new CustomerDirectoryFlow();
