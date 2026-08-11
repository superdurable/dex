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
  Attribute,
  StepList,
  booleanCodec,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "@superdurable/dex";

import { userProfileCodec, type UserProfile } from "./user-profile.js";

export const ENTITY_STORE_NAME = "entityStore";

export class UserProfileFlow implements Flow<void> {
  public readonly displayName = new Attribute("display_name", stringCodec)
    .syncToAttributeStore();
  public readonly email = new Attribute("email", stringCodec).syncToAttributeStore();
  public readonly marketingOptIn = new Attribute("marketing_opt_in", booleanCodec)
    .syncToAttributeStore();

  public getFlowType(): string {
    return "UserProfileFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.displayName, this.email, this.marketingOptIn] };
  }

  @rpc({ inputCodec: userProfileCodec })
  public updateProfile(context: Context, profile: UserProfile): void {
    this.displayName.set(context, profile.displayName);
    this.email.set(context, profile.email);
    this.marketingOptIn.set(context, profile.marketingOptIn);
  }

  @rpc({ outputCodec: userProfileCodec })
  public getProfile(context: Context): RPCResult<UserProfile> {
    return {
      output: {
        displayName: this.displayName.get(context),
        email: this.email.get(context),
        marketingOptIn: this.marketingOptIn.get(context),
      },
    };
  }

  @rpc()
  public clearProfile(context: Context): void {
    this.displayName.delete(context);
    this.email.delete(context);
    this.marketingOptIn.delete(context);
  }
}

export const userProfileFlow = new UserProfileFlow();
