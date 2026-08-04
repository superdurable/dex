/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.core;

import feign.FeignException;
import io.superdurable.dex.core.DexHttpException;
import io.superdurable.dex.core.ObjectEncoder;

// This indicates something goes wrong in the dex application
public class ClientSideException extends DexHttpException {
    public ClientSideException(final ObjectEncoder objectEncoder, final FeignException.FeignClientException exception) {
        super(objectEncoder, exception);
    }

    public ClientSideException(final DexHttpException exception) {
        super(exception);
    }
}
