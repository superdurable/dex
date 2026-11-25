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

/** In-memory stand-in for the system that keeps records of closed accounts. */
export class FakeAccountArchive {
  private readonly unavailableFlowIds = new Set<string>();
  private readonly archiveCounts = new Map<string, number>();

  public archiveAccount(flowId: string): void {
    if (this.unavailableFlowIds.has(flowId)) {
      throw new Error("account archive is unavailable");
    }
    this.archiveCounts.set(flowId, this.archiveCount(flowId) + 1);
  }

  public makeUnavailable(flowId: string): void {
    this.unavailableFlowIds.add(flowId);
  }

  public makeAvailable(flowId: string): void {
    this.unavailableFlowIds.delete(flowId);
  }

  public archiveCount(flowId: string): number {
    return this.archiveCounts.get(flowId) ?? 0;
  }
}
