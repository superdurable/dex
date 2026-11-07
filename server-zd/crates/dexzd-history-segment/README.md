# dexzd-history-segment

## Purpose

Object format of server-zd history segments, with the encoder for uploads and the decoders for history reads.

A history segment is the contiguous seq range `[first_seq, last_seq]` of one run that apply sealed.
Its object holds the run's history records and checkpoints in that range and the values superseded
inside it, as the exact row bytes stored locally. The layout is documented on
`HistorySegmentHeader` in `proto/dexzd/v1/history_segment.proto`.

- `encode_history_segment` writes one object from a `HistorySegmentContent` and returns its bytes
  and content checksum.
- `decode_history_segment_header` reads the fixed prefix and the header from the start of an
  object, so a ranged reader can then fetch only the records block, the checkpoints block, or one
  value with `decode_records_block`, `decode_checkpoints_block`, and `decode_value`.
- `decode_history_segment` reads a whole object and verifies every checksum.

Every block and every value carries an xxh3-64 of its stored bytes, and the header carries its own
xxh3-64 in the prefix, so a ranged read detects a changed or truncated object without reading the
rest. The content checksum is the xxh3-128 of the canonical uncompressed content, with each section
tagged and counted. It depends only on the stored rows, so binaries with different zstd versions
agree on it.

The format has no aggregate size limit, so every segment apply can seal is encodable. Header offset
arithmetic is checked against the object size, and a decompression's memory follows its real output
up to the declared length, so a damaged header cannot overflow or force a large allocation.

`history_segment_object_key` returns the object key of a segment.
