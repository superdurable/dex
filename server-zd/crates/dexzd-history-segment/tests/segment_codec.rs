// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Round trips through the whole-object and ranged decoders, and corruption that each must detect.

use std::ops::Range;

use dexzd_history_segment::{
    HISTORY_SEGMENT_PREFIX_BYTES, HistorySegmentBlockKind, HistorySegmentCheckpointRow,
    HistorySegmentCodecError, HistorySegmentContent, HistorySegmentIdentity,
    HistorySegmentRecordRow, HistorySegmentValueRow, decode_checkpoints_block,
    decode_history_segment, decode_history_segment_header, decode_records_block, decode_value,
    encode_history_segment,
};
use dexzd_keyspace::{RunId, ValueId};
use dexzd_protocol::dexzd::v1::HistorySegmentHeader;
use prost::Message;
use xxhash_rust::xxh3::{xxh3_64, xxh3_128};

fn value_id(run_seq: u64, index_in_command: u32) -> ValueId {
    ValueId {
        run_seq,
        index_in_command,
    }
}

fn sample_content() -> HistorySegmentContent {
    HistorySegmentContent {
        identity: HistorySegmentIdentity {
            run_id: RunId([7; 16]),
            first_seq: 10,
            last_seq: 14,
            first_event_id: 20,
            last_event_id: 31,
        },
        records: vec![
            HistorySegmentRecordRow {
                seq: 10,
                row: b"record ten with some repeated text text text".to_vec(),
            },
            HistorySegmentRecordRow {
                seq: 12,
                row: vec![0xab; 5000],
            },
            HistorySegmentRecordRow {
                seq: 14,
                row: Vec::new(),
            },
        ],
        checkpoints: vec![
            HistorySegmentCheckpointRow {
                seq: 12,
                attempt: 1,
                row: b"checkpoint of attempt one".to_vec(),
            },
            HistorySegmentCheckpointRow {
                seq: 12,
                attempt: 2,
                row: b"checkpoint of attempt two".to_vec(),
            },
        ],
        values: vec![
            HistorySegmentValueRow {
                value_id: value_id(3, 0),
                row: vec![1; 300],
            },
            HistorySegmentValueRow {
                value_id: value_id(3, 1),
                row: b"a superseded value".to_vec(),
            },
            HistorySegmentValueRow {
                value_id: value_id(11, 0),
                row: (0..=255).collect(),
            },
        ],
    }
}

fn slice(object: &[u8], range: Range<u64>) -> &[u8] {
    &object[range.start as usize..range.end as usize]
}

#[test]
fn whole_object_round_trips_and_encoding_is_stable() {
    let content = sample_content();
    let encoded = encode_history_segment(&content).unwrap();
    assert_eq!(decode_history_segment(&encoded.bytes).unwrap(), content);
    let again = encode_history_segment(&content).unwrap();
    assert_eq!(again, encoded, "the same content encodes to the same bytes");
    let layout = decode_history_segment_header(&encoded.bytes, encoded.bytes.len() as u64).unwrap();
    assert_eq!(layout.content_checksum(), encoded.content_checksum);
    assert_eq!(layout.identity(), content.identity);
    assert_eq!(layout.object_size(), encoded.bytes.len() as u64);
}

#[test]
fn ranged_reads_decode_each_block_and_one_value() {
    let content = sample_content();
    let encoded = encode_history_segment(&content).unwrap();
    let object = &encoded.bytes;
    let object_size = object.len() as u64;
    let short = decode_history_segment_header(&object[..HISTORY_SEGMENT_PREFIX_BYTES], object_size)
        .unwrap_err();
    let HistorySegmentCodecError::TruncatedHeader { required, .. } = short else {
        panic!("a prefix alone needs the header bytes too: {short:?}");
    };
    let layout = decode_history_segment_header(&object[..required], object_size).unwrap();
    let records_range = layout.records_range().unwrap();
    assert_eq!(
        decode_records_block(&layout, slice(object, records_range)).unwrap(),
        content.records
    );
    let checkpoints_range = layout.checkpoints_range().unwrap();
    assert_eq!(
        decode_checkpoints_block(&layout, slice(object, checkpoints_range)).unwrap(),
        content.checkpoints
    );
    let entry = layout.find_value(value_id(3, 1)).unwrap();
    assert_eq!(
        decode_value(
            entry,
            slice(object, layout.value_range(value_id(3, 1)).unwrap())
        )
        .unwrap(),
        content.values[1]
    );
    assert!(layout.find_value(value_id(3, 2)).is_none());
}

#[test]
fn empty_blocks_round_trip() {
    let content = HistorySegmentContent {
        checkpoints: Vec::new(),
        values: Vec::new(),
        records: vec![HistorySegmentRecordRow {
            seq: 14,
            row: b"only record".to_vec(),
        }],
        identity: HistorySegmentIdentity {
            first_event_id: 32,
            last_event_id: 31,
            ..sample_content().identity
        },
    };
    let encoded = encode_history_segment(&content).unwrap();
    assert_eq!(decode_history_segment(&encoded.bytes).unwrap(), content);
    let layout = decode_history_segment_header(&encoded.bytes, encoded.bytes.len() as u64).unwrap();
    assert!(layout.checkpoints_range().is_none());
    assert!(decode_checkpoints_block(&layout, &[]).unwrap().is_empty());
    assert!(layout.header().value_entries.is_empty());
}

#[test]
fn content_checksum_covers_rows_and_identity() {
    let content = sample_content();
    let checksum = encode_history_segment(&content).unwrap().content_checksum;
    let mut changed_value = content.clone();
    changed_value.values[2].row[0] ^= 1;
    assert_ne!(
        encode_history_segment(&changed_value)
            .unwrap()
            .content_checksum,
        checksum
    );
    let mut changed_range = content.clone();
    changed_range.identity.last_event_id += 1;
    assert_ne!(
        encode_history_segment(&changed_range)
            .unwrap()
            .content_checksum,
        checksum
    );
    let mut moved_record = content;
    moved_record.records[1].seq = 13;
    assert_ne!(
        encode_history_segment(&moved_record)
            .unwrap()
            .content_checksum,
        checksum
    );
}

#[test]
fn content_that_breaks_order_or_range_rules_is_rejected() {
    let mut out_of_range = sample_content();
    out_of_range.records[0].seq = 9;
    let mut unordered_checkpoints = sample_content();
    unordered_checkpoints.checkpoints.swap(0, 1);
    let mut duplicate_values = sample_content();
    duplicate_values.values[1].value_id = value_id(3, 0);
    let mut empty_range = sample_content();
    empty_range.identity.first_seq = 15;
    for content in [
        out_of_range,
        unordered_checkpoints,
        duplicate_values,
        empty_range,
    ] {
        let error = encode_history_segment(&content).unwrap_err();
        assert!(
            matches!(error, HistorySegmentCodecError::InvalidContent { .. }),
            "{error:?}"
        );
    }
}

#[test]
fn a_changed_prefix_is_detected() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let size = object.len() as u64;
    let mut bad_magic = object.clone();
    bad_magic[0] ^= 0xff;
    assert!(matches!(
        decode_history_segment_header(&bad_magic, size),
        Err(HistorySegmentCodecError::BadMagic)
    ));
    let mut bad_version = object.clone();
    bad_version[8] = 2;
    assert!(matches!(
        decode_history_segment_header(&bad_version, size),
        Err(HistorySegmentCodecError::UnsupportedFormatVersion { version: 2 })
    ));
    let mut bad_length = object.clone();
    bad_length[10] ^= 1;
    assert!(decode_history_segment_header(&bad_length, size).is_err());
    let mut bad_checksum = object;
    bad_checksum[14] ^= 1;
    assert!(matches!(
        decode_history_segment_header(&bad_checksum, size),
        Err(HistorySegmentCodecError::HeaderChecksumMismatch)
    ));
}

#[test]
fn a_changed_header_byte_is_detected_by_ranged_and_whole_reads() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let header_length = header_length(&object);
    for offset in [
        HISTORY_SEGMENT_PREFIX_BYTES,
        HISTORY_SEGMENT_PREFIX_BYTES + header_length / 2,
        HISTORY_SEGMENT_PREFIX_BYTES + header_length - 1,
    ] {
        let mut corrupt = object.clone();
        corrupt[offset] ^= 0x40;
        assert!(matches!(
            decode_history_segment_header(&corrupt, corrupt.len() as u64),
            Err(HistorySegmentCodecError::HeaderChecksumMismatch)
        ));
        assert!(decode_history_segment(&corrupt).is_err());
    }
}

#[test]
fn a_changed_block_or_value_byte_is_detected_by_its_ranged_read() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let layout = decode_history_segment_header(&object, object.len() as u64).unwrap();
    let records_range = layout.records_range().unwrap();
    let checkpoints_range = layout.checkpoints_range().unwrap();
    let entry = *layout.find_value(value_id(11, 0)).unwrap();
    let value_range = layout.value_range(value_id(11, 0)).unwrap();
    let flip = |range: &Range<u64>| {
        let mut corrupt = object.clone();
        corrupt[(range.start + (range.end - range.start) / 2) as usize] ^= 0x10;
        corrupt
    };

    let corrupt = flip(&records_range);
    assert!(matches!(
        decode_records_block(&layout, slice(&corrupt, records_range.clone())),
        Err(HistorySegmentCodecError::BlockChecksumMismatch {
            block: HistorySegmentBlockKind::Records
        })
    ));
    assert!(matches!(
        decode_history_segment(&corrupt),
        Err(HistorySegmentCodecError::TrailerChecksumMismatch)
    ));

    let corrupt = flip(&checkpoints_range);
    assert!(matches!(
        decode_checkpoints_block(&layout, slice(&corrupt, checkpoints_range.clone())),
        Err(HistorySegmentCodecError::BlockChecksumMismatch {
            block: HistorySegmentBlockKind::Checkpoints
        })
    ));

    let corrupt = flip(&value_range);
    assert!(matches!(
        decode_value(&entry, slice(&corrupt, value_range.clone())),
        Err(HistorySegmentCodecError::BlockChecksumMismatch {
            block: HistorySegmentBlockKind::Value(id)
        }) if id == value_id(11, 0)
    ));
    let other = layout.find_value(value_id(3, 0)).unwrap();
    assert!(
        decode_value(
            other,
            slice(&corrupt, layout.value_range(value_id(3, 0)).unwrap())
        )
        .is_ok(),
        "other values stay readable"
    );

    let short = &slice(&object, records_range)[1..];
    assert!(matches!(
        decode_records_block(&layout, short),
        Err(HistorySegmentCodecError::BlockLengthMismatch { .. })
    ));
}

#[test]
fn a_truncated_object_is_detected() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let truncated = &object[..object.len() - 5];
    assert!(matches!(
        decode_history_segment_header(truncated, truncated.len() as u64),
        Err(HistorySegmentCodecError::ObjectSizeMismatch { .. })
    ));
    assert!(matches!(
        decode_history_segment(truncated),
        Err(HistorySegmentCodecError::ObjectSizeMismatch { .. })
    ));
    let header_only = &object[..HISTORY_SEGMENT_PREFIX_BYTES + header_length(&object)];
    assert!(matches!(
        decode_history_segment(header_only),
        Err(HistorySegmentCodecError::ObjectSizeMismatch { .. })
    ));
    assert!(matches!(
        decode_history_segment(&object[..10]),
        Err(HistorySegmentCodecError::TruncatedHeader { .. })
    ));
}

#[test]
fn a_header_that_points_outside_its_blocks_is_rejected() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let moved_value = rewrite_header(&object, |header| {
        header.value_entries[1].offset += 1;
    });
    let swapped_values = rewrite_header(&object, |header| {
        header.value_entries.swap(0, 1);
    });
    let extra_record = rewrite_header(&object, |header| {
        header.record_count += 1;
    });
    let short_run_id = rewrite_header(&object, |header| {
        header.run_id.pop();
    });
    let missing_block = rewrite_header(&object, |header| {
        header.checkpoints = None;
    });
    for corrupt in [moved_value, swapped_values, short_run_id, missing_block] {
        assert!(matches!(
            decode_history_segment_header(&corrupt, corrupt.len() as u64),
            Err(HistorySegmentCodecError::InvalidHeader { .. })
        ));
    }
    let layout = decode_history_segment_header(&extra_record, extra_record.len() as u64).unwrap();
    let records = slice(&extra_record, layout.records_range().unwrap());
    assert!(matches!(
        decode_records_block(&layout, records),
        Err(HistorySegmentCodecError::MalformedBlock {
            block: HistorySegmentBlockKind::Records,
            ..
        })
    ));
}

#[test]
fn a_wrong_content_checksum_is_detected_by_whole_reads() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let corrupt = rewrite_header(&object, |header| {
        header.content_checksum[0] ^= 1;
    });
    assert!(matches!(
        decode_history_segment(&corrupt),
        Err(HistorySegmentCodecError::ContentChecksumMismatch)
    ));
}

#[test]
fn the_content_checksum_separates_records_from_checkpoints() {
    let identity = HistorySegmentIdentity {
        run_id: RunId([7; 16]),
        first_seq: 1,
        last_seq: 1,
        first_event_id: 1,
        last_event_id: 0,
    };
    // Unframed, both encode as seq 1 | 4 | 0: one record with four zero bytes, one empty
    // checkpoint of attempt 4.
    let as_record = HistorySegmentContent {
        identity,
        records: vec![HistorySegmentRecordRow {
            seq: 1,
            row: vec![0; 4],
        }],
        checkpoints: Vec::new(),
        values: Vec::new(),
    };
    let as_checkpoint = HistorySegmentContent {
        identity,
        records: Vec::new(),
        checkpoints: vec![HistorySegmentCheckpointRow {
            seq: 1,
            attempt: 4,
            row: Vec::new(),
        }],
        values: Vec::new(),
    };
    let record_checksum = encode_history_segment(&as_record).unwrap().content_checksum;
    let checkpoint_checksum = encode_history_segment(&as_checkpoint)
        .unwrap()
        .content_checksum;
    assert_ne!(record_checksum, checkpoint_checksum);
}

#[test]
fn empty_value_rows_round_trip() {
    let mut content = sample_content();
    content.values[1].row.clear();
    let encoded = encode_history_segment(&content).unwrap();
    assert_eq!(decode_history_segment(&encoded.bytes).unwrap(), content);
    let layout = decode_history_segment_header(&encoded.bytes, encoded.bytes.len() as u64).unwrap();
    let entry = layout.find_value(value_id(3, 1)).unwrap();
    assert!(layout.value_range(value_id(3, 1)).unwrap().is_empty());
    assert!(layout.value_range(value_id(3, 2)).is_none());
    assert_eq!(decode_value(entry, &[]).unwrap(), content.values[1]);
}

#[test]
fn overflowing_offsets_and_lengths_are_rejected_without_panicking() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    let changes: [fn(&mut HistorySegmentHeader); 4] = [
        |header| {
            if let Some(checkpoints) = header.checkpoints.as_mut() {
                checkpoints.length = u64::MAX;
            }
        },
        |header| {
            if let Some(values) = header.values.as_mut() {
                values.length = u64::MAX - values.offset + 1;
            }
        },
        |header| header.value_entries[2].length = u64::MAX,
        |header| {
            header.value_entries[0].uncompressed_length = u64::MAX;
            header.value_entries[1].uncompressed_length = u64::MAX;
        },
    ];
    for change in changes {
        let corrupt = rewrite_header(&object, change);
        assert!(matches!(
            decode_history_segment_header(&corrupt, corrupt.len() as u64),
            Err(HistorySegmentCodecError::InvalidHeader { .. })
        ));
    }
    let size = object.len() as u64;
    let mut huge_header = object.clone();
    huge_header[10..14].copy_from_slice(&u32::MAX.to_le_bytes());
    assert!(matches!(
        decode_history_segment_header(&huge_header, size),
        Err(HistorySegmentCodecError::ObjectSizeMismatch { .. })
    ));
    assert!(matches!(
        decode_history_segment_header(&object, u64::MAX),
        Err(HistorySegmentCodecError::ObjectSizeMismatch { .. })
    ));
}

#[test]
fn declared_lengths_and_counts_never_drive_allocation() {
    let object = encode_history_segment(&sample_content()).unwrap().bytes;
    // Values decompress one at a time, so their declared total has no aggregate limit.
    let huge_values = rewrite_header(&object, |header| {
        header.value_entries[0].uncompressed_length = 3 << 30;
        header.value_entries[1].uncompressed_length = 3 << 30;
        let total = header
            .value_entries
            .iter()
            .map(|entry| entry.uncompressed_length)
            .sum();
        if let Some(values) = header.values.as_mut() {
            values.uncompressed_length = total;
        }
    });
    let layout = decode_history_segment_header(&huge_values, huge_values.len() as u64).unwrap();
    let entry = layout.find_value(value_id(3, 0)).unwrap();
    assert!(matches!(
        decode_value(
            entry,
            slice(&huge_values, layout.value_range(value_id(3, 0)).unwrap())
        ),
        Err(HistorySegmentCodecError::Decompression { .. })
    ));
    let huge_count = rewrite_header(&object, |header| header.record_count = u32::MAX);
    let layout = decode_history_segment_header(&huge_count, huge_count.len() as u64).unwrap();
    let records = slice(&huge_count, layout.records_range().unwrap());
    assert!(matches!(
        decode_records_block(&layout, records),
        Err(HistorySegmentCodecError::MalformedBlock { .. })
    ));
}

fn header_length(object: &[u8]) -> usize {
    u32::from_le_bytes(
        object[10..14]
            .try_into()
            .expect("the prefix holds a header length"),
    ) as usize
}

/// Rewrites the header and recomputes its checksum and the trailer, as a buggy writer would.
fn rewrite_header(object: &[u8], change: impl FnOnce(&mut HistorySegmentHeader)) -> Vec<u8> {
    let header_end = HISTORY_SEGMENT_PREFIX_BYTES + header_length(object);
    let mut header =
        HistorySegmentHeader::decode(&object[HISTORY_SEGMENT_PREFIX_BYTES..header_end])
            .expect("the encoded header decodes");
    change(&mut header);
    let header_bytes = header.encode_to_vec();
    let mut rewritten = object[..10].to_vec();
    rewritten.extend_from_slice(&(header_bytes.len() as u32).to_le_bytes());
    rewritten.extend_from_slice(&xxh3_64(&header_bytes).to_le_bytes());
    rewritten.extend_from_slice(&header_bytes);
    rewritten.extend_from_slice(&object[header_end..object.len() - 16]);
    let trailer = xxh3_128(&rewritten);
    rewritten.extend_from_slice(&trailer.to_le_bytes());
    rewritten
}
