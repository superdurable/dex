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

import type { Client } from "@superdurable/dex";

import { cronScheduleFlow } from "../patterns/cron/cron-schedule-flow.js";
import { isFlowAlreadyStarted } from "../service-errors.js";

export const CRON_SCHEDULE_FLOW_ID = "cron-schedule-sample";

export async function startCronSchedule(client: Client): Promise<void> {
  try {
    await client.startFlow(cronScheduleFlow, CRON_SCHEDULE_FLOW_ID, {
      interval: { value: 1, unit: "hour" as const },
      runCount: 10,
    });
  } catch (error) {
    if (isFlowAlreadyStarted(error)) {
      return;
    }
    throw error;
  }
}
