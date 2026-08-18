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

export class ServiceDependency {
  private attemptFailures = 0;

  public attemptExternalApiCall(message: string): string {
    if (this.attemptFailures < 2) {
      this.attemptFailures += 1;
      throw new Error("There is an error when calling external system, retry it");
    }
    this.attemptFailures = 0;
    console.log(`attemptExternalApiCall: ${message}`);
    return "External data result";
  }

  public externalApiCall(message: string): string {
    console.log(`externalApiCall: ${message}`);
    return "External data result";
  }

  public updateExternalSystem(message: string): void {
    console.log(`update external system via RPC/Kafka/DB: ${message}`);
  }

  public sendEmail(subject: string, content: string): void {
    console.log(`send email subject=${subject} content=${content}`);
  }

  public upsert(document: unknown): void {
    console.log(`upsert: ${JSON.stringify(document)}`);
  }
}

export const serviceDependency = new ServiceDependency();
