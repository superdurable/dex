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

import { Router } from "express";

import { InitialAttribute, type Client } from "@superdurable/dex";

import { startOptions } from "../../config/env.js";
import { profileFromRequest, requiredString } from "../shared/http-helpers.js";
import {
  ENTITY_STORE_NAME,
  userProfileFlow,
} from "./user-profile-flow.js";
import type { UserProfileRequest } from "./user-profile.js";

export function createEntityStoreRouter(client: Client): Router {
  const router = Router();

  router.post("/profile", async (request, response) => {
    const body = request.body as UserProfileRequest;
    const userId = requiredString(body.userId, "userId");
    const profile = profileFromRequest(body);
    await client.startFlow(userProfileFlow, userId, undefined, startOptions({
      attributes: [
        InitialAttribute.of(userProfileFlow.displayName, profile.displayName),
        InitialAttribute.of(userProfileFlow.email, profile.email),
        InitialAttribute.of(
          userProfileFlow.marketingOptIn,
          profile.marketingOptIn,
        ),
        InitialAttribute.of(userProfileFlow.credits, BigInt(profile.credits)),
        InitialAttribute.of(userProfileFlow.weight, profile.weight),
        InitialAttribute.of(
          userProfileFlow.lastLoggedInTime,
          profile.lastLoggedInTime,
        ),
        InitialAttribute.of(userProfileFlow.metadata, profile.metadata),
      ],
      configOverride: { attributeStoreName: ENTITY_STORE_NAME },
    }));
    response.status(201).json({ userId, ...profile });
  });

  router.post("/profile/update", async (request, response) => {
    const body = request.body as UserProfileRequest;
    const userId = requiredString(body.userId, "userId");
    const profile = profileFromRequest(body);
    await client.invokeRPC(userProfileFlow.updateProfile, userId, profile);
    response.json({ userId, ...profile });
  });

  router.get("/profile", async (request, response) => {
    const userId = requiredString(request.query.userId, "userId");
    const profile = await client.invokeRPC(userProfileFlow.getProfile, userId);
    response.json({ userId, ...profile });
  });

  router.post("/profile/clear", async (request, response) => {
    const userId = requiredString(request.query.userId, "userId");
    await client.invokeRPC(userProfileFlow.clearProfile, userId);
    response.send("cleared");
  });

  return router;
}
