// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import { readResponseJSON } from './http';

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
});

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}
