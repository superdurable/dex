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

import { jsonCodec } from "@superdurable/dex";

export const Status = Object.freeze({
  INITIATED: "Initiated",
  ACCEPTED: "Accepted",
  DECLINED: "Declined",
} as const);

export type Status = (typeof Status)[keyof typeof Status];

export interface EngagementInput {
  employerId: string;
  jobSeekerId: string;
  notes: string;
}

export interface EngagementDescription {
  employerId: string;
  jobSeekerId: string;
  notes: string;
  currentStatus: Status;
}

export function decodeStatus(value: unknown): Status {
  const status = String(value);
  if (status === Status.INITIATED || status === Status.ACCEPTED || status === Status.DECLINED) {
    return status;
  }
  throw new Error(`unknown status: ${status}`);
}

export const statusCodec = jsonCodec<Status>({
  typeName: "Status",
  decode: decodeStatus,
});
