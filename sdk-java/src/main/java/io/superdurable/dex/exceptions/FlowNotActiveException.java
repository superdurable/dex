/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

import io.grpc.Status;

public final class FlowNotActiveException extends DexException {
    public FlowNotActiveException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.FLOW_NOT_EXISTS, detail, cause);
    }
}
