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

import { DexServiceError } from "@superdurable/dex";

const grpcNotFound = 5;
const grpcAlreadyExists = 6;

export function isFlowAlreadyStarted(error: unknown): boolean {
  return error instanceof DexServiceError && error.code === grpcAlreadyExists;
}

export function isFlowMissingOrInactive(error: unknown): boolean {
  return error instanceof DexServiceError && error.code === grpcNotFound;
}
