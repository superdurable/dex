// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This file defines the private, versioned format shared by normal reads and
// startup recovery. Its fixed header records blob-ID and payload lengths plus a
// CRC32C covering both data sections; reserved bytes must remain zero for
// future evolution. Payload bytes are intentionally opaque, so string and
// EncodedObject interpretation stays in the hydration layer. For example, a
// truncated file, overflowing declared length, changed blob ID, or corrupted
// payload is rejected before untrusted lengths can trigger an unsafe
// allocation.

package blobcache

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
)

const (
	fileMagic             = "DXBC"
	fileVersion     uint8 = 1
	fixedHeaderSize       = 24
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type fileHeader struct {
	blobIDLength  uint32
	payloadLength uint64
	checksum      uint32
}

func calculateMetadata(blobID string, payload []byte, path string) (fileMetadata, error) {
	if err := validateBlobID(blobID); err != nil {
		return fileMetadata{}, err
	}
	if uint64(len(payload)) > math.MaxInt64 {
		return fileMetadata{}, fmt.Errorf("%w: payload length overflows int64", ErrInvalidBlob)
	}

	size := int64(fixedHeaderSize) + int64(len(blobID))
	if int64(len(payload)) > math.MaxInt64-size {
		return fileMetadata{}, fmt.Errorf("%w: complete file size overflows int64", ErrInvalidBlob)
	}
	size += int64(len(payload))

	return fileMetadata{
		blobID:   blobID,
		path:     path,
		size:     size,
		checksum: calculateChecksum(blobID, payload),
	}, nil
}

func validateBlobID(blobID string) error {
	if blobID == "" {
		return fmt.Errorf("%w: blob ID must not be empty", ErrInvalidBlob)
	}
	if len(blobID) > maxBlobIDBytes {
		return fmt.Errorf("%w: blob ID exceeds %d bytes", ErrInvalidBlob, maxBlobIDBytes)
	}
	return nil
}

func calculateChecksum(blobID string, payload []byte) uint32 {
	checksum := crc32.New(crcTable)
	writeChecksumPrefix(checksum, blobID)
	_, err := checksum.Write(payload)
	if err != nil {
		panic(err)
	}
	return checksum.Sum32()
}

func writeChecksumPrefix(checksum hash.Hash32, blobID string) {
	_, err := io.WriteString(checksum, blobID)
	if err != nil {
		panic(err)
	}
}

func encodeHeader(header fileHeader) []byte {
	encoded := make([]byte, fixedHeaderSize)
	copy(encoded[0:4], fileMagic)
	encoded[4] = fileVersion
	binary.LittleEndian.PutUint32(encoded[8:12], header.blobIDLength)
	binary.LittleEndian.PutUint64(encoded[12:20], header.payloadLength)
	binary.LittleEndian.PutUint32(encoded[20:24], header.checksum)
	return encoded
}

func decodeHeader(encoded []byte) (fileHeader, error) {
	if len(encoded) != fixedHeaderSize {
		return fileHeader{}, fmt.Errorf("%w: header length %d", ErrCorrupt, len(encoded))
	}
	if string(encoded[0:4]) != fileMagic {
		return fileHeader{}, fmt.Errorf("%w: invalid magic", ErrCorrupt)
	}
	if encoded[4] != fileVersion {
		return fileHeader{}, fmt.Errorf("%w: unsupported version %d", ErrCorrupt, encoded[4])
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return fileHeader{}, fmt.Errorf("%w: non-zero reserved bits", ErrCorrupt)
	}

	return fileHeader{
		blobIDLength:  binary.LittleEndian.Uint32(encoded[8:12]),
		payloadLength: binary.LittleEndian.Uint64(encoded[12:20]),
		checksum:      binary.LittleEndian.Uint32(encoded[20:24]),
	}, nil
}
