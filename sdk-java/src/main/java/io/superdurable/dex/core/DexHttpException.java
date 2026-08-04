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
import io.superdurable.dex.core.exceptions.LongPollTimeoutException;
import io.superdurable.dex.core.exceptions.NoRunningWorkflowException;
import io.superdurable.dex.core.exceptions.WorkflowAlreadyStartedException;
import io.superdurable.dex.gen.models.EncodedObject;
import io.superdurable.dex.gen.models.ErrorResponse;
import io.superdurable.dex.gen.models.ErrorSubStatus;

import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.util.Optional;

public abstract class DexHttpException extends RuntimeException {

    private final int statusCode;
    private ErrorResponse errorResponse;

    public DexHttpException(final ObjectEncoder objectEncoder, final FeignException.FeignClientException exception) {
        super(exception);
        statusCode = exception.status();
        String decodeErrorMessage = "";
        final Optional<ByteBuffer> respBody = exception.responseBody();
        if (respBody.isPresent()) {
            String data = StandardCharsets.UTF_8.decode(respBody.get()).toString();
            try {
                errorResponse = objectEncoder.decode(new EncodedObject().data(data), ErrorResponse.class);
                return;
            } catch (Exception e) {
                decodeErrorMessage = e.getMessage();
            }
        }
        errorResponse = new ErrorResponse()
                .detail("empty or unable to decode to ErrorResponse:" + decodeErrorMessage)
                .subStatus(ErrorSubStatus.UNCATEGORIZED_SUB_STATUS);
    }

    protected DexHttpException(final DexHttpException exception) {
        statusCode = exception.getStatusCode();
        errorResponse = exception.getErrorResponse();
    }

    public DexHttpException() {
        statusCode = 500;
    }

    public String getErrorDetails() {
        return errorResponse.getDetail();
    }

    public int getStatusCode() {
        return statusCode;
    }

    public ErrorSubStatus getErrorSubStatus() {
        return errorResponse.getSubStatus();
    }

    public ErrorResponse getErrorResponse() {
        return errorResponse;
    }

    public static DexHttpException fromFeignException(final ObjectEncoder objectEncoder, final FeignException.FeignClientException exception) {
        if (exception.status() >= 400 && exception.status() < 500) {
            final ClientSideException clientSideException = new ClientSideException(objectEncoder, exception);

            switch (clientSideException.getErrorSubStatus()) {
                case LONG_POLL_TIME_OUT_SUB_STATUS:
                    return new LongPollTimeoutException(clientSideException);
                case WORKFLOW_ALREADY_STARTED_SUB_STATUS:
                    return new WorkflowAlreadyStartedException(clientSideException);
                case WORKFLOW_NOT_EXISTS_SUB_STATUS:
                    return new NoRunningWorkflowException(clientSideException);
                default:
                    return clientSideException;
            }
        } else {
            return new ServerSideException(objectEncoder, exception);
        }
    }
}
