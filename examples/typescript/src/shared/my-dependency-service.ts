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

export class MyDependencyService {
  public updateExternalSystem(message: string): void {
    console.log(`update external system via RPC/Kafka/DB: ${message}`);
  }

  public sendEmail(recipient: string, subject: string, content: string): void {
    console.log(`send email to=${recipient} subject=${subject} content=${content}`);
  }

  public chargeUser(email: string, customerId: string, amount: number): void {
    console.log(`charge user email=${email} customerId=${customerId} amount=${amount}`);
  }

  public shipItem(orderId: string, testFailAtShipping: boolean): void {
    if (testFailAtShipping) {
      throw new Error(`ship failed for order ${orderId}`);
    }
    console.log(`ship item ${orderId}`);
  }

  public callAPI1(data: string): void {
    console.log(`call API1 ${data}`);
  }

  public callAPI2(data: string): void {
    console.log(`call API2 ${data}`);
  }

  public callAPI3(data: string): void {
    console.log(`call API3 ${data}`);
  }

  public callAPI4(data: string): void {
    console.log(`call API4 ${data}`);
  }

  public checkBalance(_account: string, _amount: number): boolean {
    return true;
  }

  public debit(_account: string, _amount: number): void {}

  public credit(_account: string, _amount: number): void {}

  public createDebitMemo(_account: string, _amount: number, _notes: string): void {}

  public createCreditMemo(_account: string, _amount: number, _notes: string): void {}

  public undoDebit(_account: string, _amount: number): void {}

  public undoCredit(_account: string, _amount: number): void {}

  public undoCreateDebitMemo(_account: string, _amount: number, _notes: string): void {}

  public undoCreateCreditMemo(_account: string, _amount: number, _notes: string): void {}
}

export const myDependencyService = new MyDependencyService();
