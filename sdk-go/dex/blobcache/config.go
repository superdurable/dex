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

package blobcache

import (
	"errors"
	"fmt"
)

const (
	defaultFrequencyCounters int64 = 10_000
	defaultBufferItems       int64 = 64
	maxBlobIDBytes                 = 1 << 20
)

var (
	// ErrClosed indicates an operation on a closed cache.
	ErrClosed = errors.New("blob cache is closed")
	// ErrInvalidConfig indicates invalid cache configuration.
	ErrInvalidConfig = errors.New("invalid blob cache configuration")
	// ErrInvalidBlob indicates invalid blob metadata.
	ErrInvalidBlob = errors.New("invalid blob")
	// ErrContentMismatch indicates reused blob IDs with different content.
	ErrContentMismatch = errors.New("blob ID content mismatch")
	// ErrCorrupt indicates a malformed cache file.
	ErrCorrupt = errors.New("corrupt blob cache entry")
	// ErrReconciliation indicates the disk budget could not be restored.
	ErrReconciliation = errors.New("blob cache reconciliation failed")
)

// Config configures one exclusively owned disk cache directory.
type Config struct {
	// Dir has no default and must name an exclusively owned cache directory.
	Dir string
	// MaxBytes has no default; it limits payloads plus cache-file metadata on disk.
	MaxBytes int64
	// FrequencyCounters defaults to 10,000; use roughly 10× expected blobs; Ristretto uses about 3 bytes per counter before rounding.
	FrequencyCounters int64
}

func validateConfig(cfg *Config) error {
	if cfg.Dir == "" {
		return fmt.Errorf("%w: Dir must not be empty", ErrInvalidConfig)
	}
	if cfg.MaxBytes <= 0 {
		return fmt.Errorf("%w: MaxBytes must be positive", ErrInvalidConfig)
	}
	if cfg.FrequencyCounters < 0 {
		return fmt.Errorf("%w: FrequencyCounters must not be negative", ErrInvalidConfig)
	}
	if cfg.FrequencyCounters == 0 {
		cfg.FrequencyCounters = defaultFrequencyCounters
	}
	return nil
}
