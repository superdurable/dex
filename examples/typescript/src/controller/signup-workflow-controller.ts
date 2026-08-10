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

import { FlowAlreadyStartedError, type Client } from "@superdurable/dex";

import { startOptions } from "../config/env.js";
import type { SignupForm } from "../workflow/signup/signup-form.js";
import { userSignupFlow } from "../workflow/signup/user-signup-flow.js";

export function createSignupRouter(client: Client): Router {
  const router = Router();

  router.get("/submit", async (request, response) => {
    const username = String(request.query.username ?? "");
    const email = String(request.query.email ?? "");
    const form: SignupForm = {
      username,
      email,
      firstName: "Test",
      lastName: "Test",
    };
    try {
      await client.startFlow(userSignupFlow, username, form, startOptions());
    } catch (failure) {
      if (failure instanceof FlowAlreadyStartedError) {
        response.send("username already started registry");
        return;
      }
      throw failure;
    }
    response.send("success");
  });

  router.get("/verify", async (request, response) => {
    const username = String(request.query.username ?? "");
    const result = await client.invokeRPC(
      userSignupFlow.verifySignup,
      username,
    );
    response.send(result);
  });

  return router;
}
