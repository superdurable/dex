// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
)

const (
	// maxFlowNameScanBytes bounds how much of one oversized directory file Dex Web reads to find flow.name.
	maxFlowNameScanBytes = 64 << 20
	// maxFlowNameScanBytesPerLoad bounds the oversized-file bytes that one definition request reads.
	maxFlowNameScanBytesPerLoad = 256 << 20
	// maxFlowObjectBytes bounds the top-level flow object that the scan decodes.
	maxFlowObjectBytes = 1 << 20
	// maxScannedFlowNames bounds the remembered scan results; the cache starts over when it is full.
	maxScannedFlowNames = 1024
	// maxScannedKeyBytes is the longest top-level key the scan keeps; any longer key is not "flow".
	maxScannedKeyBytes = 64
)

var errFlowNameScanBudgetExhausted = errors.New("this request already read its limit of oversized files")

// oversizedFlowNames remembers each oversized file's flow.name by path, size, and modification time. Every
// target in the process shares it, because one unchanged file always gives the same answer.
var oversizedFlowNames = &flowNameScanCache{results: make(map[flowNameScanKey]flowNameScanResult)}

type flowNameScanKey struct {
	path              string
	size              int64
	modifiedUnixNanos int64
}

type flowNameScanResult struct {
	flowName string
	err      error
}

type flowNameScanCache struct {
	mu      sync.Mutex
	results map[flowNameScanKey]flowNameScanResult
}

// flowNameScanBudget is what one definition request may still read of oversized files.
type flowNameScanBudget struct {
	remainingBytes int64
}

// scan returns the flow.name of an oversized file, reading it only when it changed since the last scan and
// the request's budget allows.
func (cache *flowNameScanCache) scan(filePath string, info fs.FileInfo, budget *flowNameScanBudget) (string, error) {
	key := flowNameScanKey{path: filePath, size: info.Size(), modifiedUnixNanos: info.ModTime().UnixNano()}
	cache.mu.Lock()
	result, isCached := cache.results[key]
	cache.mu.Unlock()
	if isCached {
		return result.flowName, result.err
	}
	cost := min(info.Size(), maxFlowNameScanBytes)
	if budget.remainingBytes < cost {
		return "", errFlowNameScanBudgetExhausted
	}
	budget.remainingBytes -= cost
	result.flowName, result.err = scanFlowNameFile(filePath)
	cache.mu.Lock()
	if len(cache.results) >= maxScannedFlowNames {
		clear(cache.results)
	}
	cache.results[key] = result
	cache.mu.Unlock()
	return result.flowName, result.err
}

func scanFlowNameFile(filePath string) (flowName string, returnErr error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	return scanTopLevelFlowName(io.LimitReader(file, maxFlowNameScanBytes))
}

// scanTopLevelFlowName reads the top-level keys of a JSON object byte by byte, skipping every other value
// without keeping it, and returns the name of the last top-level flow object, as json.Unmarshal would. When
// the reader ends early, it returns the last name it found, or an error when it found none.
func scanTopLevelFlowName(source io.Reader) (string, error) {
	reader := bufio.NewReaderSize(source, 64<<10)
	opening, err := readNonSpaceByte(reader)
	if err != nil {
		return "", err
	}
	if opening != '{' {
		return "", fmt.Errorf("the document is not a JSON object")
	}
	flowName := ""
	isFound := false
	for {
		next, err := readNonSpaceByte(reader)
		if err != nil {
			return truncatedFlowNameScan(flowName, isFound, err)
		}
		if next == '}' {
			if !isFound {
				return "", fmt.Errorf("the document has no flow object")
			}
			return flowName, nil
		}
		if next != '"' {
			return "", fmt.Errorf("the document has a malformed key")
		}
		key, err := readScannedKey(reader)
		if err != nil {
			return truncatedFlowNameScan(flowName, isFound, err)
		}
		if colon, err := readNonSpaceByte(reader); err != nil || colon != ':' {
			return truncatedFlowNameScan(flowName, isFound, errors.Join(err, fmt.Errorf("the document has a key without a value")))
		}
		if key == "flow" {
			var flowObject bytes.Buffer
			if err := consumeJSONValue(reader, &flowObject, maxFlowObjectBytes); err != nil {
				return truncatedFlowNameScan(flowName, isFound, err)
			}
			var flow struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(flowObject.Bytes(), &flow); err != nil {
				return "", fmt.Errorf("decode the flow object: %w", err)
			}
			flowName, isFound = strings.TrimSpace(flow.Name), true
		} else if err := consumeJSONValue(reader, nil, 0); err != nil {
			return truncatedFlowNameScan(flowName, isFound, err)
		}
		separator, err := readNonSpaceByte(reader)
		if err != nil {
			return truncatedFlowNameScan(flowName, isFound, err)
		}
		if separator == '}' {
			if !isFound {
				return "", fmt.Errorf("the document has no flow object")
			}
			return flowName, nil
		}
		if separator != ',' {
			return "", fmt.Errorf("the document has a malformed object")
		}
	}
}

func truncatedFlowNameScan(flowName string, isFound bool, err error) (string, error) {
	if isFound {
		return flowName, nil
	}
	if errors.Is(err, io.EOF) {
		return "", fmt.Errorf("no flow object within the first %d bytes", maxFlowNameScanBytes)
	}
	return "", err
}

// readScannedKey reads a key after its opening quote, keeping at most maxScannedKeyBytes of it.
func readScannedKey(reader *bufio.Reader) (string, error) {
	var raw []byte
	isLong := false
	for {
		current, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if current == '"' {
			break
		}
		if current == '\\' {
			escaped, err := reader.ReadByte()
			if err != nil {
				return "", err
			}
			raw = append(raw, current, escaped)
		} else {
			raw = append(raw, current)
		}
		if len(raw) > maxScannedKeyBytes {
			isLong, raw = true, raw[:0]
		}
	}
	if isLong {
		return "", nil
	}
	var key string
	if err := json.Unmarshal(append(append([]byte{'"'}, raw...), '"'), &key); err != nil {
		return "", fmt.Errorf("decode a key: %w", err)
	}
	return key, nil
}

// consumeJSONValue reads one value. With a capture buffer it keeps the value's bytes up to limit; without one
// it keeps nothing, so a large string or a deep array costs no memory.
func consumeJSONValue(reader *bufio.Reader, capture *bytes.Buffer, limit int) error {
	write := func(value byte) error {
		if capture == nil {
			return nil
		}
		if capture.Len() >= limit {
			return fmt.Errorf("the flow object is larger than %d bytes", limit)
		}
		return capture.WriteByte(value)
	}
	first, err := readNonSpaceByte(reader)
	if err != nil {
		return err
	}
	if err := write(first); err != nil {
		return err
	}
	switch first {
	case '"':
		return consumeJSONString(reader, write)
	case '{', '[':
		return consumeJSONContainer(reader, write)
	default:
		return consumeJSONScalar(reader, write)
	}
}

// consumeJSONString reads a string after its opening quote.
func consumeJSONString(reader *bufio.Reader, write func(byte) error) error {
	isEscaped := false
	for {
		current, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if err := write(current); err != nil {
			return err
		}
		switch {
		case isEscaped:
			isEscaped = false
		case current == '\\':
			isEscaped = true
		case current == '"':
			return nil
		}
	}
}

// consumeJSONContainer reads an object or array after its opening bracket, counting depth without recursion.
func consumeJSONContainer(reader *bufio.Reader, write func(byte) error) error {
	depth := 1
	for depth > 0 {
		current, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if err := write(current); err != nil {
			return err
		}
		switch current {
		case '"':
			if err := consumeJSONString(reader, write); err != nil {
				return err
			}
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return nil
}

// consumeJSONScalar reads a number, true, false, or null; the delimiter after it stays unread.
func consumeJSONScalar(reader *bufio.Reader, write func(byte) error) error {
	for {
		current, err := reader.ReadByte()
		if err != nil {
			return err
		}
		switch current {
		case ',', '}', ']', ' ', '\n', '\r', '\t':
			return reader.UnreadByte()
		}
		if err := write(current); err != nil {
			return err
		}
	}
}

func readNonSpaceByte(reader *bufio.Reader) (byte, error) {
	for {
		current, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if current != ' ' && current != '\n' && current != '\r' && current != '\t' {
			return current, nil
		}
	}
}
