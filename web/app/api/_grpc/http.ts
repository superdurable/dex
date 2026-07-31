import * as grpc from '@grpc/grpc-js';
import { NextResponse } from 'next/server';

export function grpcErrorResponse(error: unknown, operation: string) {
  const serviceError = error as Partial<grpc.ServiceError>;
  const code = serviceError.code;
  const status = code === grpc.status.INVALID_ARGUMENT
    ? 400
    : code === grpc.status.NOT_FOUND
      ? 404
      : code === grpc.status.FAILED_PRECONDITION
        ? 409
        : code === grpc.status.DEADLINE_EXCEEDED
          ? 408
          : 502;
  return NextResponse.json(
    {
      error: serviceError.details || serviceError.message || `${operation} failed`,
      grpcCode: code,
    },
    { status },
  );
}
