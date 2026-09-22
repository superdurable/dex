// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { DexAPIError, isTransientGatewayResponse, readResponseJSON } from './http';

describe('isTransientGatewayResponse', () => {
  it('recognizes temporary gateway responses', () => {
    for (const statusCode of [502, 503, 504]) {
      expect(isTransientGatewayResponse(new Response(null, { status: statusCode }))).toBe(true);
    }
    expect(isTransientGatewayResponse(new Response(null, { status: 408 }))).toBe(false);
    expect(isTransientGatewayResponse(new Response(null, { status: 500 }))).toBe(false);
  });
});

describe('readResponseJSON', () => {
  it('returns parsed JSON for a successful response', async () => {
    const response = jsonResponse({ flowId: 'order-1' }, 200);
    await expect(readResponseJSON<{ flowId: string }>(response)).resolves.toEqual({
      flowId: 'order-1',
    });
  });

  it('uses the API error field when the response is not ok', async () => {
    const response = jsonResponse({ error: 'Flow not found' }, 404);
    await expect(readResponseJSON(response)).rejects.toThrow('Flow not found');
  });

  it('explains an empty body instead of a JSON parse failure', async () => {
    const response = new Response('', {
      status: 500,
      statusText: 'Internal Server Error',
      headers: { 'content-type': 'text/plain' },
    });
    Object.defineProperty(response, 'url', {
      value: 'http://127.0.0.1:5173/api/flows/summary?flowId=order-1',
    });

    await expect(readResponseJSON(response)).rejects.toThrow(
      'Dex API returned an empty response (HTTP 500) for /api/flows/summary?flowId=order-1. The Dex server may be unreachable.',
    );
  });

  it('explains a non-JSON body and includes a short snippet', async () => {
    const response = new Response('Error: connect ECONNREFUSED 127.0.0.1:8802', {
      status: 500,
    });
    Object.defineProperty(response, 'url', {
      value: 'http://127.0.0.1:5173/api/flows/search',
    });

    await expect(readResponseJSON(response)).rejects.toThrow(
      'Dex API returned a non-JSON response (HTTP 500) for /api/flows/search: Error: connect ECONNREFUSED 127.0.0.1:8802',
    );
  });

  it('carries the gRPC code and HTTP status a Dex error body reported', async () => {
    const response = jsonResponse({ error: 'Flow is not active', grpcCode: 9 }, 409);
    const failure = await readResponseJSON(response).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(DexAPIError);
    expect(failure).toBeInstanceOf(Error);
    const dexFailure = failure as DexAPIError;
    expect(dexFailure.message).toBe('Flow is not active');
    expect(dexFailure.grpcCode).toBe(9);
    expect(dexFailure.httpStatus).toBe(409);
  });

	it('carries the definition recovery code', async () => {
		const response = jsonResponse({
			error: 'Flow Definition updated',
			code: 'FLOW_DEFINITION_CHANGED',
		}, 409);
		const failure = (await readResponseJSON(response).catch((error: unknown) => error)) as DexAPIError;
		expect(failure.code).toBe('FLOW_DEFINITION_CHANGED');
		expect(failure.httpStatus).toBe(409);
	});

  it('leaves the gRPC code undefined rather than zero when the body omits it', async () => {
    const response = jsonResponse({ error: 'Flow not found' }, 404);
    const failure = (await readResponseJSON(response).catch((error: unknown) => error)) as DexAPIError;
    expect(failure.grpcCode).toBeUndefined();
    expect(failure.httpStatus).toBe(404);
  });

  it('keeps the generated message when the body carries a code but no error text', async () => {
    const response = jsonResponse({ grpcCode: 14 }, 502);
    Object.defineProperty(response, 'url', { value: 'http://127.0.0.1:5173/api/v2/display' });
    const failure = (await readResponseJSON(response).catch((error: unknown) => error)) as DexAPIError;
    expect(failure.message).toBe('Dex API returned an error (HTTP 502) for /api/v2/display');
    expect(failure.grpcCode).toBe(14);
  });
});

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}
