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

import { FlowNotActiveError, type Client } from "@superdurable/dex";

import type { EmployerOptInFlow } from "./employer-opt-in-flow.js";

export function employerOptIn(employerId: string): string {
  return `shortlist_candidates_opt_in_${employerId}`;
}

export function shortlist(employerId: string, candidateId: string): string {
  return `shortlist_candidates_shortlist_${employerId}_${candidateId}`;
}

export async function isOptedIn(
  client: Client,
  employerOptInFlow: EmployerOptInFlow,
  employerId: string,
): Promise<boolean> {
  try {
    return Boolean(
      await client.invokeRPC(employerOptInFlow.isOptedIn, employerOptIn(employerId)),
    );
  } catch (failure) {
    if (failure instanceof FlowNotActiveError) {
      return false;
    }
    throw failure;
  }
}
