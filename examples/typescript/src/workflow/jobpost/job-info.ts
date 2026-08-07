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

import { jsonCodec, optionalCodec, stringCodec } from "@superdurable/dex";

export interface JobInfo {
  title: string;
  description: string;
  notes: string;
}

export function decodeJobInfo(value: unknown): JobInfo {
  const record = value as JobInfo;
  return {
    title: String(record.title),
    description: String(record.description),
    notes: String(record.notes ?? ""),
  };
}

export const jobInfoCodec = jsonCodec<JobInfo>({
  typeName: "JobInfo",
  decode: decodeJobInfo,
});

export const optionalJobInfoCodec = optionalCodec(jobInfoCodec);

export const optionalStringCodec = optionalCodec(stringCodec);
