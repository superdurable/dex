# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import json
import math
import pathlib
import sys
root = pathlib.Path(__file__).resolve().parent
paths = sorted(root.glob('*/search.json'))
seen = set()
total_queries = 0
total_samples = 0
required = {'default-page-1', 'common-flow-type', 'rare-flow-type', 'start-time-desc', 'keyword-equality', 'int-range', 'keyword-array-contains', 'text-contains', 'default-page-500'}
for path in paths:
    record = json.loads(path.read_text())
    config = record['config']
    identity = (config['backend'], config['runs'], config['attributes'], config['backfill'])
    assert identity not in seen, identity
    seen.add(identity)
    assert not record.get('error'), (path, record.get('error'))
    assert record['seed_seconds'] > 0 and record['catchup_seconds'] >= 0, path
    assert math.isclose(record['rows_per_second'], config['runs'] / (record['seed_seconds'] + record['catchup_seconds']), rel_tol=0.0001), path
    if config['backend'] != 'builtin':

        def parse_wal_lsn(text):
            high, low = text.split('/')
            return int(high, 16) * 2 ** 32 + int(low, 16)
        assert record['wal_bytes_per_row'] > 0, path
        expected = (parse_wal_lsn(record['wal_end']) - parse_wal_lsn(record['wal_start'])) / config['runs']
        assert math.isclose(record['wal_bytes_per_row'], expected, rel_tol=1e-10), (path, expected)
    queries = record.get('queries') or []
    if config['attributes'] == 10:
        ready = [query for query in queries if query['phase'] == 'ready']
        assert {query['name'] for query in ready} >= required, path
        for query in queries:
            assert not query.get('errors') and (not query.get('warmup_errors')), (path, query)
            assert query['samples'] > 0, (path, query)
            assert query['samples'] == len(query['raw_ms']), (path, query)
            assert query['first_page_rows'] == 20 and query['last_page_rows'] == 20, (path, query)
            values = query['latency']
            assert 0 < values['p50_ms'] <= values['p90_ms'] <= values['p99_ms'] <= values['max_ms'], (path, query)
            if query['phase'] == 'ready':
                assert query['samples'] == config['samples'], (path, query)
            total_samples += query['samples']
            total_queries += 1
    else:
        assert config['projection_only'] and (not queries), path
    if config['backfill']:
        assert record['backfill_seconds'] > 0, path
        assert {query['name'] for query in queries if query['phase'] == 'backfilling'} == {'keyword-equality', 'keyword-common-equality'}, path
if '--partial' not in sys.argv:
    expected = {(backend, runs, attributes, False) for backend in ['builtin', 'postgres', 'paradedb'] for runs in [100000, 1000000] for attributes in [0, 3, 10]}
    expected.add(('postgres', 1000000, 10, True))
    assert seen == expected, (expected - seen, seen - expected)
print(f'Validated {len(paths)} completed primary points, {total_queries} query distributions, and {total_samples} measured successful requests; no measured errors or recorded warmup failures.')
