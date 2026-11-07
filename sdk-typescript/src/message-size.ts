// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ChannelOptions } from "@grpc/grpc-js";

/**
 * The gRPC receive limit, in bytes, of a Client or Worker whose `grpcMaxMessageBytes` is zero or omitted.
 *
 * The limit applies to every message the SDK receives: Client responses such as RPC results and
 * state reads, responses to the Worker's FlowService calls such as LoadBlobs, and the Step and RPC
 * tasks that Dex sends to a Worker. The default is 32 MiB, which matches the Dex server's default
 * `api.grpcMaxMessageBytes`. With the default, the SDK keeps gRPC's unlimited send limit, so the
 * Dex server's receive limit bounds what the SDK sends. A positive `grpcMaxMessageBytes` limits
 * both directions.
 *
 * A message carries one or more application values plus an envelope of metadata. Keep the limit
 * above the largest application value by an explicit margin. The default leaves 16 MiB of headroom
 * for values up to 16 MiB. When an application needs larger values, raise both
 * `grpcMaxMessageBytes` and the server's `api.grpcMaxMessageBytes`, because each hop applies the
 * smaller limit of its ends.
 *
 * @example
 * ```ts
 * const client = new Client(registry, cache, {
 *   grpcMaxMessageBytes: 2 * DEFAULT_GRPC_MAX_MESSAGE_BYTES,
 * });
 * ```
 * @since 1.38.0
 */
export const DEFAULT_GRPC_MAX_MESSAGE_BYTES = 32 * 1024 * 1024;

export interface GrpcMessageLimits {
  readonly receiveBytes: number;
  readonly channelOptions: ChannelOptions;
}

/**
 * Resolves a `grpcMaxMessageBytes` option into gRPC channel options.
 * @param configured - Zero for the default receive limit and unlimited sends; positive to limit both directions.
 * @returns The effective receive limit and the matching channel options.
 * @throws {RangeError} When `configured` is negative or not an integer.
 */
export function resolveGrpcMessageLimits(configured: number): GrpcMessageLimits {
  if (!Number.isSafeInteger(configured) || configured < 0) {
    throw new RangeError(`grpcMaxMessageBytes must be a non-negative integer, got ${configured}`);
  }
  if (configured === 0) {
    return {
      receiveBytes: DEFAULT_GRPC_MAX_MESSAGE_BYTES,
      channelOptions: { "grpc.max_receive_message_length": DEFAULT_GRPC_MAX_MESSAGE_BYTES },
    };
  }
  return {
    receiveBytes: configured,
    channelOptions: {
      "grpc.max_receive_message_length": configured,
      "grpc.max_send_message_length": configured,
    },
  };
}
