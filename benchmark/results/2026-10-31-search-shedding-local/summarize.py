# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Summarize archived benchmark logs and exact before/after projection counters."""

import gzip
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent
COUNTERS = (
    "dexzd_search_projection_coalesced_changes_total",
    "dexzd_search_projection_dropped_updates_total",
    "dexzd_search_projection_rebuild_requests_total",
    "dexzd_search_skipped_documents_total",
)
OVERFLOW = "a committed frame could not be queued for the search projection; rebuilding its partition"
ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")


def read_counters(path):
    samples = {}
    for line in path.read_text().splitlines():
        if line.startswith("#") or not line.strip():
            continue
        name, value = line.split()[:2]
        if name in COUNTERS:
            samples[name] = int(value)
    assert set(samples) == set(COUNTERS), (path, samples)
    return samples


def summarize(point):
    archive = point / "server.log.gz"
    compressed = archive.read_bytes()
    lines = ANSI.sub("", gzip.decompress(compressed).decode()).splitlines()
    before = read_counters(point / "metrics-before.openmetrics")
    after = read_counters(point / "metrics-after.openmetrics")
    result = json.loads((point / "search.json").read_text())
    assert not result.get("error"), result.get("error")
    assert result["config"]["attributes"] == 10
    assert result["config"]["projection_only"]
    assert result["rows_per_second"] > 0
    assert not result["queries"]
    start_high, start_low = (int(part, 16) for part in result["wal_start"].split("/"))
    end_high, end_low = (int(part, 16) for part in result["wal_end"].split("/"))
    wal_bytes = ((end_high << 32) + end_low) - ((start_high << 32) + start_low)
    assert abs(wal_bytes / result["config"]["runs"] - result["wal_bytes_per_row"]) < 0.00001
    assert abs(result["rows_per_second"] * (result["seed_seconds"] + result["catchup_seconds"]) - result["config"]["runs"]) < 1
    summary = {
        "source_file": archive.name,
        "source_sha256": hashlib.sha256(compressed).hexdigest(),
        "source_compressed_bytes": len(compressed),
        "line_count": len(lines),
        "projection_queue_overflow_count": sum(OVERFLOW in line for line in lines),
        "error_level_line_count": sum(" ERROR " in line for line in lines),
        "overflow_match": OVERFLOW,
        "error_level_match": " ERROR ",
        "metrics_before_file": "metrics-before.openmetrics",
        "metrics_after_file": "metrics-after.openmetrics",
        "projection_counter_deltas": {name: after[name] - before[name] for name in COUNTERS},
    }
    assert summary["error_level_line_count"] == 0, point
    assert all(value >= 0 for value in summary["projection_counter_deltas"].values()), summary
    assert summary["projection_counter_deltas"]["dexzd_search_skipped_documents_total"] == 0
    counts = (point / "projected-counts.txt").read_text().strip()
    runs = result["config"]["runs"]
    assert counts == f"{runs}|{runs // 20}", (point, counts)
    assert (point / "container-removal.txt").read_text().splitlines() == [result["config"]["database_container"]]
    (point / "server-log-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    return summary


if __name__ == "__main__":
    points = sorted(ROOT.glob("*-a10"))
    assert len(points) == 4, points
    for point in points:
        summary = summarize(point)
        print(point.name, json.dumps(summary["projection_counter_deltas"], sort_keys=True))
    print("Validated four ten-Attribute seed points, counters, projected counts, throughput arithmetic, and container removal.")
