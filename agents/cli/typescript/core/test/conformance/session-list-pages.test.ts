// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  collectSessionsForList,
  sessionListRequestLimit,
  sessionListRowLimit,
  type SessionListPage,
} from "../../src/session-list-pages.js";

const sharedCliDirectory = fileURLToPath(new URL("../../../../../", import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(sharedCliDirectory, "..", "conformance", "cli_session_list.json"), "utf8"),
) as SessionListFixture;

interface SessionListFixture {
  readonly row_limit: number;
  readonly request_limit: number;
  readonly cases: readonly {
    readonly name: string;
    readonly pages: readonly { readonly count: number; readonly has_next_page_token: boolean }[];
    readonly requests: number;
    readonly rows: number;
    readonly has_more: boolean;
  }[];
}

describe("sessions list pages: the shared cases", () => {
  test("the limits match", () => {
    assert.equal(sessionListRowLimit, fixture.row_limit);
    assert.equal(sessionListRequestLimit, fixture.request_limit);
  });

  for (const testCase of fixture.cases) {
    test(testCase.name, async () => {
      const requestedTokens: string[] = [];
      const loadPage = (nextPageToken: string): Promise<SessionListPage<string>> => {
        const pageNumber = requestedTokens.length;
        requestedTokens.push(nextPageToken);
        const page = testCase.pages[pageNumber];
        assert.ok(page !== undefined, `request ${pageNumber + 1} goes past the last page`);
        return Promise.resolve({
          sessions: Array.from({ length: page.count }, (_, index) => `page-${pageNumber}-session-${index}`),
          nextPageToken: page.has_next_page_token ? `token-${pageNumber + 1}` : "",
        });
      };

      const collected = await collectSessionsForList(loadPage);

      assert.equal(requestedTokens.length, testCase.requests);
      assert.deepEqual(
        requestedTokens,
        requestedTokens.map((_, index) => (index === 0 ? "" : `token-${index}`)),
      );
      const expectedSessions = testCase.pages
        .slice(0, testCase.requests)
        .flatMap((page, pageNumber) =>
          Array.from({ length: page.count }, (_, index) => `page-${pageNumber}-session-${index}`),
        )
        .slice(0, testCase.rows);
      assert.deepEqual(collected.sessions, expectedSessions);
      assert.equal(collected.hasMoreSessions, testCase.has_more);
    });
  }
});
