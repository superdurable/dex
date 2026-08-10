/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import com.google.protobuf.InvalidProtocolBufferException;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.ErrorSubStatus;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.gen.ErrorResponse;

final class GrpcExceptionTranslator {
    enum FlowTargetRequirement {
        NONE,
        EXISTING,
        ACTIVE
    }

    private GrpcExceptionTranslator() {
    }

    static DexServiceException translate(
            final StatusRuntimeException exception,
            final FlowTargetRequirement requirement,
            final String flowId) {
        final ErrorResponse details;
        try {
            details = unpackDetails(exception);
        } catch (InvalidProtocolBufferException malformed) {
            final DexServiceException translated = new DexServiceException(
                    exception.getStatus().getCode(),
                    ErrorSubStatus.UNCATEGORIZED,
                    "Dex returned malformed error details",
                    exception);
            translated.addSuppressed(malformed);
            return translated;
        }

        final Status.Code code = exception.getStatus().getCode();
        final String detail = errorDetail(exception, details);
        final ErrorSubStatus subStatus = details == null
                ? ErrorSubStatus.UNCATEGORIZED
                : mapSubStatus(details.getSubStatus());
        switch (subStatus) {
            case FLOW_ALREADY_STARTED:
                return new FlowAlreadyStartedException(code, detail, exception);
            case FLOW_NOT_EXISTS:
                return missingFlowException(code, detail, exception, requirement);
            case WORKER_API_ERROR:
                return workerException(code, detail, exception, details);
            case LONG_POLL_TIMEOUT:
                return new LongPollTimeoutException(code, detail, flowId, exception);
            default:
                return new DexServiceException(code, subStatus, detail, exception);
        }
    }

    private static ErrorResponse unpackDetails(
            final StatusRuntimeException exception)
            throws InvalidProtocolBufferException {
        final com.google.rpc.Status status = StatusProto.fromThrowable(exception);
        if (status == null) {
            return null;
        }
        for (com.google.protobuf.Any value : status.getDetailsList()) {
            if (value.is(ErrorResponse.class)) {
                return value.unpack(ErrorResponse.class);
            }
        }
        return null;
    }

    private static String errorDetail(
            final StatusRuntimeException exception,
            final ErrorResponse details) {
        if (details != null && !details.getDetail().isEmpty()) {
            return details.getDetail();
        }
        if (exception.getStatus().getDescription() != null
                && !exception.getStatus().getDescription().isEmpty()) {
            return exception.getStatus().getDescription();
        }
        return exception.getStatus().getCode().name();
    }

    private static DexServiceException missingFlowException(
            final Status.Code code,
            final String detail,
            final StatusRuntimeException cause,
            final FlowTargetRequirement requirement) {
        if (requirement == FlowTargetRequirement.EXISTING) {
            return new FlowNotFoundException(code, detail, cause);
        }
        if (requirement == FlowTargetRequirement.ACTIVE) {
            return new FlowNotActiveException(code, detail, cause);
        }
        return new DexServiceException(
                code,
                ErrorSubStatus.FLOW_NOT_EXISTS,
                detail,
                cause);
    }

    private static DexServiceException workerException(
            final Status.Code code,
            final String detail,
            final StatusRuntimeException cause,
            final ErrorResponse details) {
        if (code == Status.Code.ABORTED) {
            return new RpcLockConflictException(detail, cause);
        }
        final String workerType = details == null
                ? ""
                : details.getOriginalWorkerErrorType();
        final String workerDetail = details == null
                ? ""
                : details.getOriginalWorkerErrorDetail();
        final String workerStackTrace = details == null
                ? ""
                : details.getOriginalWorkerErrorStackTrace();
        final Status.Code workerCode = details == null
                || (workerType.isEmpty()
                && workerDetail.isEmpty()
                && workerStackTrace.isEmpty()
                && details.getOriginalWorkerErrorStatus() == 0)
                ? null
                : Status.fromCodeValue(details.getOriginalWorkerErrorStatus()).getCode();
        return new WorkerInvocationException(
                code,
                detail,
                workerType,
                workerDetail,
                workerStackTrace,
                workerCode,
                cause);
    }

    private static ErrorSubStatus mapSubStatus(
            final io.superdurable.gen.ErrorSubStatus subStatus) {
        switch (subStatus) {
            case ERROR_SUB_STATUS_FLOW_ALREADY_STARTED:
                return ErrorSubStatus.FLOW_ALREADY_STARTED;
            case ERROR_SUB_STATUS_FLOW_NOT_EXISTS:
                return ErrorSubStatus.FLOW_NOT_EXISTS;
            case ERROR_SUB_STATUS_WORKER_API_ERROR:
                return ErrorSubStatus.WORKER_API_ERROR;
            case ERROR_SUB_STATUS_LONG_POLL_TIME_OUT:
                return ErrorSubStatus.LONG_POLL_TIMEOUT;
            default:
                return ErrorSubStatus.UNCATEGORIZED;
        }
    }
}
