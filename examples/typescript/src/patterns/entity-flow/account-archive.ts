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

/**
 * In-memory stand-in for the system that keeps records of closed accounts.
 * Archiving is idempotent per account, as a unique insert keyed by the Flow ID would be.
 */
export class FakeAccountArchive {
  private readonly unavailableFlowIds = new Set<string>();
  private readonly lostResponseFlowIds = new Set<string>();
  private readonly archiveRecords = new Map<string, string>();
  private readonly attemptStepExecutionIds = new Map<string, string[]>();

  public archiveAccount(flowId: string, stepExecutionId: string): void {
    const attempts = this.attemptStepExecutionIds.get(flowId) ?? [];
    attempts.push(stepExecutionId);
    this.attemptStepExecutionIds.set(flowId, attempts);
    if (this.archiveRecords.has(flowId)) {
      return;
    }
    if (this.unavailableFlowIds.has(flowId)) {
      throw new Error("account archive is unavailable");
    }
    this.archiveRecords.set(flowId, stepExecutionId);
    if (this.lostResponseFlowIds.delete(flowId)) {
      throw new Error("account archive response was lost");
    }
  }

  public makeUnavailable(flowId: string): void {
    this.unavailableFlowIds.add(flowId);
  }

  public makeAvailable(flowId: string): void {
    this.unavailableFlowIds.delete(flowId);
  }

  public loseNextResponse(flowId: string): void {
    this.lostResponseFlowIds.add(flowId);
  }

  public archiveRecordCount(flowId: string): number {
    return this.archiveRecords.has(flowId) ? 1 : 0;
  }

  public attemptStepExecutionIdsFor(flowId: string): readonly string[] {
    return [...(this.attemptStepExecutionIds.get(flowId) ?? [])];
  }
}
