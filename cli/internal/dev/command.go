// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dev

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

func Execute(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	supervisor := newSupervisor(cfg, stdout, stderr)
	if err := supervisor.Run(ctx); err != nil {
		return fmt.Errorf("Dex development environment failed: %w", err)
	}
	return nil
}
