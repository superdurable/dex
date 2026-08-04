// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
