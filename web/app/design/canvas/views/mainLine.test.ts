// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { longestPathRanks, type GraphEdge } from './mainLine';

function edge(from: string, to: string): GraphEdge {
  return { id: `${from}->${to}`, from, to, failure: false };
}

describe('longestPathRanks', () => {
  it('ignores a back edge and ranks the rest by longest path', () => {
    const ranks = longestPathRanks(['a', 'b', 'c', 'd'], [edge('a', 'b'), edge('b', 'c'), edge('c', 'a'), edge('a', 'c'), edge('c', 'd')]);
    expect([...ranks.entries()]).toEqual([['a', 0], ['b', 1], ['c', 2], ['d', 3]]);
  });

  it('ranks a chain far longer than the call stack allows recursion for', () => {
    const ids = Array.from({ length: 50_000 }, (_, index) => `s${index}`);
    const edges = ids.slice(1).map((id, index) => edge(ids[index], id));
    edges.push(edge(ids[ids.length - 1], ids[0]));
    const ranks = longestPathRanks(ids, edges);
    expect(ranks.get(ids[ids.length - 1])).toBe(ids.length - 1);
  });
});
